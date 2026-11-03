"""A RackBill notifier implemented entirely in Python. Sends no messages."""

import json
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent / "_generated"))

from grpclib.const import Status
from grpclib.exceptions import GRPCError

from contract import CONTRACT_VERSION
from plugin.proto import notifier_pb2 as notifier
from plugin.proto import notifier_grpc
from plugin.proto import provisioner_pb2 as shared
from runtime import log, serve


class ExampleNotifier(notifier_grpc.NotifierBase):
    # One object handles concurrent calls from all tenants. Never retain a
    # request's config, credentials, recipient or message on this object.
    def describe(self):
        # Pure metadata: do not contact a provider here.
        return notifier.DescribeNotifierResponse(
            name="python-example",
            display_name="Python example notifier (simulation)",
            version="0.1.0",  # This plugin's version, independent of the SDK.
            maturity="dev",
            channels=["email"],
            config_fields=[shared.ConfigField(
                key="simulate", label="Simulated outcome", type="select",
                options=[
                    shared.ConfigFieldOption(
                        value="", label="Provider rejection",
                        description="Return a failed result without sending a message.",
                    ),
                    shared.ConfigFieldOption(
                        value="error", label="Transport error",
                        description="Return an RPC error without contacting a provider.",
                    ),
                ],
            )],
        )

    async def DescribeNotifier(self, stream):
        await stream.recv_message()
        await stream.send_message(self.describe())

    async def Send(self, stream):
        request = await stream.recv_message()
        if request.channel != "email":
            await stream.send_message(notifier.SendResponse(
                status="failed", failure_reason="This example supports email only."
            ))
            return

        # Stderr carries hclog-shaped JSON; stdout belongs to the handshake.
        # Never log recipients, message bodies, credentials or the config map.
        log("debug", "Simulating notification delivery", stream, channel="email")

        # Configuration is supplied on EVERY call. A real implementation calls
        # its provider here with an async client. grpclib cancels this coroutine
        # on client cancellation/deadline; stream.deadline.time_remaining() gives
        # the remaining timeout when a deadline was supplied. Do not swallow
        # asyncio.CancelledError or block the event loop with synchronous I/O.
        # Return status="sent" plus the real provider_id only after acceptance.
        outcome = request.config.get("simulate", "")
        if outcome == "error":
            raise GRPCError(Status.UNAVAILABLE, "example only: simulated transport failure")
        reason = (
            "Example only: message delivery is not implemented."
            if outcome == "" else "Unknown simulated outcome."
        )
        await stream.send_message(notifier.SendResponse(status="failed", failure_reason=reason))

    # Advertise capability flags only after implementing the associated RPCs.
    async def ListConfigOptions(self, stream):
        raise GRPCError(Status.UNIMPLEMENTED)

    async def GetChatMember(self, stream):
        raise GRPCError(Status.UNIMPLEMENTED)

    async def ModifyChatMemberRoles(self, stream):
        raise GRPCError(Status.UNIMPLEMENTED)


def main():
    plugin = ExampleNotifier()
    if sys.argv[1:] == ["-rb-plugin-info"]:
        info = plugin.describe()
        print(json.dumps({
            "schema": 1, "kind": "notifier", "name": info.name,
            "display_name": info.display_name, "version": info.version,
            "maturity": info.maturity, "contract": CONTRACT_VERSION,
        }))
        return
    if sys.argv[1:]:
        raise SystemExit("usage: notifier-python-example [-rb-plugin-info]")
    serve(plugin)


if __name__ == "__main__":
    main()
