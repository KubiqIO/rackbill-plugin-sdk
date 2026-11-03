# Example notifier plugin

This directory contains equivalent **Go and Python** RackBill plugin scaffolds.
Both declare their identity and configuration, then handle rendered notifications.
The Go version uses `sdk.ServeNotifier`; the Python version implements the wire
protocol directly with no Go wrapper or Go runtime requirement.

The example simulates a provider rejection by default. Selecting **Transport
error** returns an error instead. It sends no messages and reports no successful
deliveries. Use it as a starting point for your own provider integration.

## Go: build and inspect

From the SDK repository root, using the Go version required by `go.mod`:

```sh
go build -o /tmp/notifier-example ./examples/notifier-example
/tmp/notifier-example -rb-plugin-info
go test ./...
```

The inspection command prints JSON containing `kind: "notifier"`, `name:
"example"`, the plugin version and the SDK contract number. Normal startup is
managed by RackBill; running the binary without this flag is not a standalone
notification test.

There is no separate `go.mod` here. Keeping the example inside the SDK module
means the SDK's regular build, vet and test commands compile it too.

## Go: start your own plugin

From the SDK repository root, copy the implementation into a new module:

```sh
mkdir ../notifier-acme
cp examples/notifier-example/main.go ../notifier-acme/main.go
cd ../notifier-acme
go mod init example.com/notifier-acme
go get rackbill.com/sdk@v0.2.0
go mod tidy
```

Choose an SDK tag from the [published releases](https://github.com/KubiqIO/rackbill-plugin-sdk/releases)
that supports the oldest RackBill agent you target. The command targets SDK
`v0.2.0`; installation requires that release to be available.
The SDK and its public dependencies are sufficient; no RackBill source checkout
is needed.

When adapting `main.go`:

- Change the plugin type, `Name`, `DisplayName` and plugin `Version`. Keep the
  binary's `notifier-` prefix and match its suffix to `Name`.
- Replace `ConfigFields` with the provider's settings. Use `Type: "secret"` for
  credentials and read values from each call's `in.Config`.
- Replace simulated delivery with the provider call. Use the supplied context
  for cancellation; `sdk.HTTPClient` supplies SDK logging for HTTP integrations.
- Return `sdk.SendSent` and the real `ProviderID` after the provider accepts the
  message. Represent a provider rejection with `sdk.SendFailed`; return an error
  when the provider cannot be reached.
- Keep `Describe` free of external calls. Keep the plugin safe for concurrent
  requests from different tenants. Use `sdk.Log` for diagnostics and reserve
  stdout for the SDK protocol.
- Change the declared maturity to reflect your implementation. This example's
  `sdk.MaturityDev` marks its simulated behavior.

After changing `Name` to `"acme"`, build and inspect your plugin:

```sh
go build -o notifier-acme .
./notifier-acme -rb-plugin-info
```

Plugin versions, SDK release versions and SDK contract numbers are independent.
The SDK automatically includes its contract in the binary. Its corresponding
agent requirement is available through
`sdk.AgentForContract(sdk.ContractVersion)` and in that SDK release's notes.

See the [plugin author guide](https://rackbill.com/docs/plugins/) and
[notification provider guide](https://rackbill.com/docs/plugins/notifiers/).
The [SDK license](../../LICENSE) also applies to both versions of this example.

## Python: build and inspect

The Python version is an opt-in source example. Normal SDK builds and tests do
not run it or install its packages. Set up the environment below only when you
want to run or adapt the Python plugin; its interoperability test requires the
explicit `python_example` Go build tag.

Requires Python 3.11+ with `venv` and `pip`, on Linux or macOS. From this
directory in an SDK checkout:

```sh
python3 -m venv .venv
.venv/bin/python -m pip install --upgrade grpcio-tools grpclib protobuf cryptography
.venv/bin/python build.py
./dist/notifier-python-example -rb-plugin-info
.venv/bin/python -B -m unittest discover -s . -p 'test_*.py'
```

Package versions are intentionally unpinned for this source example. Install
them only when running it; no Python dependency manifest or lockfile is
maintained for the sample.

No Go compiler or separately installed `protoc` is needed. `build.py` generates
Python messages and service bindings into `_generated/` from the SDK's existing
`notifier.proto` and `provisioner.proto` (which defines shared config fields).
It also stamps `ContractVersion` from that same SDK snapshot, and writes a
launcher at `dist/notifier-python-example`. Generated files and virtual
environments are ignored by Git.

The metadata command works without the host cookie and reports kind `notifier`,
name `python-example`, plugin version `0.1.0`, maturity `dev`, and the selected
SDK's contract. The distinct name allows the Go and Python examples to coexist.
Choose an SDK release whose minimum agent version supports your installation;
do not manually lower the generated contract number.

| File | Purpose |
|---|---|
| `main.py` | Plugin identity, config form, `DescribeNotifier`, `Send`, optional RPC refusals and `-rb-plugin-info` |
| `runtime.py` | go-plugin handshake, mutual TLS, health probe, shutdown and JSON logging |
| `build.py` | Protobuf generation, contract stamp and executable launcher |
| `test_startup.py` | Startup refusal and logging checks |
| `interop_test.go` | Optional Go host test of the Python subprocess; excluded from normal Go builds |

### Load it in RackBill

Keep the example directory and its virtual environment in a stable location
readable by the RackBill process. The launcher contains **absolute paths** to
the virtual environment's interpreter and `main.py`, so it works without an
activated environment, `PYTHONPATH`, or a particular working directory. Rebuild
after moving the source directory, and recreate the venv if it moves.

Put a symlink to the launcher in your operator-managed `RB_PLUGINS_DIR` (default
`plugins-dist/`, relative to the agent's working directory), then restart RackBill:

```sh
# Run from this example directory. Replace the destination with your plugin directory.
ln -s "$PWD/dist/notifier-python-example" /path/to/plugins-dist/notifier-python-example
```

Use an installation whose license allows ordinary plugins; an unlicensed
installation loads only its built-in `<kind>-dev` simulators. `maturity: dev`
labels the example's simulated behavior and does not bypass that loading gate.
The default mutual TLS setting works without changing `RB_PLUGIN_MTLS`.

The launcher is a script, not a portable single-file executable. Python, its
dependencies and the source/generated files must exist **inside the agent's
execution environment**. For a container deployment, provision them in a custom
image and build the launcher using paths inside that image; a host-side venv
cannot be used from a container by copying only the launcher.

### What the Python runtime implements

The host starts the process with `RACKBILL_PLUGIN=rackbill-gateway-v1` and
application protocol version `1`. After binding a loopback port, the plugin
flushes exactly one handshake line to stdout:

```text
1|1|tcp|127.0.0.1:PORT|grpc|SERVER_CERTIFICATE
```

`SERVER_CERTIFICATE` is unpadded standard base64 of a fresh DER certificate.
When the host supplies `PLUGIN_CLIENT_CERT`, the server trusts only that
certificate and requires client authentication. The server certificate names
`localhost`, as expected by go-plugin. Malformed certificates fail startup.
Without a supplied certificate, the host has disabled AutoMTLS and the listener
uses plaintext loopback. Ephemeral server key/cert files have mode `0600` and are
removed immediately after loading them into Python's TLS context.

The runtime uses [grpclib](https://grpclib.readthedocs.io/en/latest/server.html)
with Python's OpenSSL TLS support: the host's P-521 client certificate must be
accepted, which the default BoringSSL signature preferences in `grpcio`'s server
do not support. `grpcio-tools` is used only for protobuf generation.

`grpc.health.v1.Health/Check` answers for service `plugin`, and
`plugin.GRPCController/Shutdown`, SIGINT and SIGTERM stop the process.
Optional bundle, storage, widget, broker and stdio probes return `UNIMPLEMENTED`.
The example logs hclog-shaped JSON directly to stderr; the host supplies the
initial level via `RACKBILL_LOG_LEVEL` and updates it per call through
`rackbill-log-level` metadata. Never print diagnostics to stdout.

This is a small serving example, not a complete Python SDK. Reverse host storage,
widgets, multi-kind bundles, provider HTTP logging/redaction and packaging a
standalone executable require additional implementation.

### Start your own Python plugin

Copy `main.py`, `runtime.py`, `build.py` and `.gitignore` into your own project.
Keep a pinned SDK checkout available at build time, then run:

```sh
python3 -m venv .venv
.venv/bin/python -m pip install --upgrade grpcio-tools grpclib protobuf cryptography
.venv/bin/python build.py --sdk-root /absolute/path/to/rackbill-plugin-sdk
```

Change the identity and configuration in `main.py` and the launcher's filename
in `build.py`, keeping the `notifier-` prefix. Replace simulated delivery with
an async provider call, reading credentials from each request's `config`. Mark
credential fields as `secret`; never retain tenant settings between requests.
Respect cancellation and the RPC deadline, keep blocking I/O off the event loop,
and redact provider diagnostics before logging them. Return `status="sent"` and
the provider's actual message ID only after acceptance; return a `failed` result
for rejection and raise `GRPCError` for transport failure. RackBill owns retries.

### Verify interoperability with the Go host

These checks are local and opt-in. They are not part of the repository's default
test suite or SDK publication, so no example-specific Python environment needs
to be maintained on a CI runner.

After building the Python example, optionally run from the SDK root using the
Go toolchain required by `go.mod`:

```sh
go test -tags=python_example -count=1 -timeout=60s ./examples/notifier-example
```

The Go test launches the Python process from an unrelated directory with a
minimal environment. It checks metadata, default mutual TLS, refusal of clients
without the host certificate, plaintext opt-out, health, concurrent request
isolation, failed results versus RPC errors, optional probes and shutdown.
The plugin itself remains entirely Python.

Protocol references: [RackBill wire protocol](https://rackbill.com/docs/plugins/wire-protocol/)
and [go-plugin's non-Go guide](https://github.com/hashicorp/go-plugin/blob/v1.8.0/docs/guide-plugin-write-non-go.md).
