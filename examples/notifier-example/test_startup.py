"""Failure paths must exit without publishing a usable handshake."""

import json
import os
from pathlib import Path
import subprocess
import unittest


class StartupTests(unittest.TestCase):
    def run_plugin(self, **env):
        launcher = Path(__file__).resolve().parent / "dist/notifier-python-example"
        return subprocess.run(
            [str(launcher)], cwd="/tmp", env={"PATH": os.defpath, **env},
            capture_output=True, text=True, timeout=10,
        )

    def test_cookie_is_required(self):
        for env in ({}, {"RACKBILL_PLUGIN": "wrong"}):
            with self.subTest(env=env):
                result = self.run_plugin(**env)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")
                self.assertIn("Let RackBill start it", result.stderr)

    def test_incompatible_protocol(self):
        result = self.run_plugin(
            RACKBILL_PLUGIN="rackbill-gateway-v1", PLUGIN_PROTOCOL_VERSIONS="2,3",
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertIn("protocol version 1", result.stderr)

    def test_bad_certificate_does_not_fall_back_to_plaintext(self):
        result = self.run_plugin(
            RACKBILL_PLUGIN="rackbill-gateway-v1", PLUGIN_CLIENT_CERT="invalid certificate",
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")

    def test_log_level_metadata_and_safe_fields(self):
        import asyncio
        from contextlib import redirect_stderr
        from io import StringIO
        from unittest.mock import AsyncMock, Mock, patch

        from main import ExampleNotifier, notifier

        stream = Mock()
        stream.metadata = {"rackbill-log-level": "debug"}
        request = notifier.SendRequest(
            channel="email", to="private@example.test", subject="private subject",
            body="private body", html_body="private HTML", config={"password": "private password"},
        )
        stream.recv_message = AsyncMock(return_value=request)
        stream.send_message = AsyncMock()
        with patch.dict(os.environ, {"RACKBILL_LOG_LEVEL": "error"}), redirect_stderr(StringIO()) as stderr:
            asyncio.run(ExampleNotifier().Send(stream))
            record = json.loads(stderr.getvalue())
            self.assertEqual(record["@level"], "debug")
            self.assertEqual(record["channel"], "email")
            self.assertNotIn("private", stderr.getvalue())

        stream.metadata = {"rackbill-log-level": "error"}
        with patch.dict(os.environ, {"RACKBILL_LOG_LEVEL": "debug"}), redirect_stderr(StringIO()) as stderr:
            asyncio.run(ExampleNotifier().Send(stream))
            self.assertEqual(stderr.getvalue(), "")


if __name__ == "__main__":
    unittest.main()
