"""The small part of go-plugin's serving protocol needed by this example.

This is a single-kind server. Bundle, widgets and reverse storage/broker RPCs
are optional and deliberately left UNIMPLEMENTED. It is not a full Python SDK.
"""

import asyncio
import base64
from datetime import datetime, timedelta, timezone
import json
import os
import signal
import socket
import ssl
import sys
import tempfile
import threading

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID
from google.protobuf.empty_pb2 import Empty
from grpclib.const import Cardinality, Handler, Status
from grpclib.exceptions import GRPCError
from grpclib.health.v1.health_grpc import HealthBase
from grpclib.health.v1.health_pb2 import HealthCheckResponse
from grpclib.server import Server


_log_lock = threading.Lock()
_levels = {"debug": 10, "info": 20, "warn": 30, "error": 40}


def log(level, message, stream=None, **fields):
    configured = os.environ.get("RACKBILL_LOG_LEVEL", "info")
    if stream is not None:
        configured = stream.metadata.get("rackbill-log-level", configured)
    if _levels[level] < _levels.get(configured, 20):
        return
    record = {
        **fields,
        "@level": level,
        "@message": message,
        "@timestamp": datetime.now(timezone.utc).isoformat(timespec="microseconds"),
    }
    with _log_lock:
        print(json.dumps(record), file=sys.stderr, flush=True)


def tls_context(client_pem):
    # Trust only the one-time certificate supplied by this host. A malformed
    # certificate must fail startup, never cause a downgrade to plaintext.
    x509.load_pem_x509_certificate(client_pem)
    key = ec.generate_private_key(ec.SECP256R1())
    name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "localhost")])
    now = datetime.now(timezone.utc)
    cert = (
        x509.CertificateBuilder()
        .subject_name(name).issuer_name(name).public_key(key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(now - timedelta(seconds=30))
        # Like go-plugin, use a long validity period: an otherwise healthy
        # plugin must not stop accepting connections after running for a day.
        # Keys are fresh for each process.
        .not_valid_after(now + timedelta(hours=262980))
        .add_extension(x509.SubjectAlternativeName([x509.DNSName("localhost")]), critical=False)
        .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True)
        .add_extension(x509.KeyUsage(
            digital_signature=True, content_commitment=False, key_encipherment=False,
            data_encipherment=False, key_agreement=False, key_cert_sign=True,
            crl_sign=True, encipher_only=False, decipher_only=False,
        ), critical=True)
        .add_extension(x509.ExtendedKeyUsage([ExtendedKeyUsageOID.SERVER_AUTH]), critical=False)
        .sign(key, hashes.SHA256())
    )
    key_pem = key.private_bytes(
        serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8,
        serialization.NoEncryption(),
    )
    cert_pem = cert.public_bytes(serialization.Encoding.PEM)
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.minimum_version = ssl.TLSVersion.TLSv1_2
    context.verify_mode = ssl.CERT_REQUIRED
    context.load_verify_locations(cadata=client_pem.decode())
    context.set_alpn_protocols(["h2"])
    # Python's OpenSSL API loads keys from files. NamedTemporaryFile creates
    # private (0600) files, deleted immediately after OpenSSL reads them.
    with tempfile.NamedTemporaryFile() as cert_file, tempfile.NamedTemporaryFile() as key_file:
        cert_file.write(cert_pem)
        key_file.write(key_pem)
        cert_file.flush()
        key_file.flush()
        context.load_cert_chain(cert_file.name, key_file.name)
    # go-plugin expects unpadded standard base64 of DER, not PEM or base64url.
    handshake_cert = base64.b64encode(cert.public_bytes(serialization.Encoding.DER)).decode().rstrip("=")
    return context, handshake_cert


class Health(HealthBase):
    async def Check(self, stream):
        request = await stream.recv_message()
        if request.service not in ("", "plugin"):
            raise GRPCError(Status.NOT_FOUND)
        await stream.send_message(HealthCheckResponse(status=HealthCheckResponse.SERVING))

    async def Watch(self, stream):
        raise GRPCError(Status.UNIMPLEMENTED)


class Controller:
    def __init__(self, shutdown):
        self.shutdown = shutdown

    async def Shutdown(self, stream):
        await stream.recv_message()
        await stream.send_message(Empty())
        await stream.send_trailing_metadata()
        self.shutdown.set()

    def __mapping__(self):
        # plugin.Empty and google.protobuf.Empty have identical wire encodings.
        return {"/plugin.GRPCController/Shutdown": Handler(
            self.Shutdown, Cardinality.UNARY_UNARY, Empty, Empty,
        )}


class UnsupportedServices:
    async def unimplemented(self, stream):
        raise GRPCError(Status.UNIMPLEMENTED)

    def __mapping__(self):
        # Explicit handlers give optional probes proper gRPC trailers. grpclib
        # 0.4.9's unknown-route response omits content-type, which grpc-go rejects
        # as UNKNOWN instead of the UNIMPLEMENTED needed by capability discovery.
        # No request is decoded and no response is sent, so Empty is sufficient.
        routes = {
            "/rackbill.plugin.v1.Bundle/DescribeBundle": Cardinality.UNARY_UNARY,
            "/rackbill.plugin.v1.WidgetProvider/DescribeWidgets": Cardinality.UNARY_UNARY,
            "/rackbill.plugin.v1.StorageManifest/DescribeStorage": Cardinality.UNARY_UNARY,
            "/plugin.GRPCStdio/StreamStdio": Cardinality.UNARY_STREAM,
            "/plugin.GRPCBroker/StartStream": Cardinality.STREAM_STREAM,
        }
        return {path: Handler(self.unimplemented, cardinality, Empty, Empty)
                for path, cardinality in routes.items()}


def serve(plugin):
    if os.environ.get("RACKBILL_PLUGIN") != "rackbill-gateway-v1":
        raise SystemExit("This is a RackBill plugin. Let RackBill start it, or use -rb-plugin-info.")
    offered = os.environ.get("PLUGIN_PROTOCOL_VERSIONS", "1").split(",")
    if "1" not in offered:
        raise SystemExit("This plugin requires application protocol version 1.")

    asyncio.run(_serve(plugin))


async def _serve(plugin):
    shutdown = asyncio.Event()
    server = Server([plugin, Health(), Controller(shutdown), UnsupportedServices()])
    # go-plugin tolerates UNIMPLEMENTED for GRPCStdio and GRPCBroker.
    # Logs go directly to stderr; this plugin needs no reverse RPCs.
    client_cert = os.environ.pop("PLUGIN_CLIENT_CERT", "")
    context, handshake_cert = None, ""
    if client_cert:
        context, handshake_cert = tls_context(client_cert.encode())
    # An absent client cert means the host has explicitly disabled AutoMTLS.
    for sig in (signal.SIGINT, signal.SIGTERM):
        asyncio.get_running_loop().add_signal_handler(sig, shutdown.set)
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
        await server.start(sock=listener, ssl=context)
        try:
            # CORE | APP | NETWORK | ADDRESS | TRANSPORT | SERVER CERTIFICATE
            print(f"1|1|tcp|127.0.0.1:{port}|grpc|{handshake_cert}", flush=True)
            await shutdown.wait()
        finally:
            server.close()
            await server.wait_closed()
