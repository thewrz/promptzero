"""Process tests for the dependency-free MCP smoke client."""

from __future__ import annotations

import json
import os
import pathlib
import subprocess
import sys
import tempfile
import time
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
CLIENT = ROOT / "scripts/mcp-smoke.py"


class SmokeClientTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.server = self.root / "fake-mcp-server"
        self.server.write_text(
            "#!/usr/bin/python3\n"
            "import json, os, sys\n"
            "with open(os.environ['FAKE_REQUESTS'], 'w') as f:\n"
            " for line in sys.stdin:\n"
            "  req=json.loads(line); f.write(json.dumps(req)+'\\n'); f.flush()\n"
            "  if req.get('method') == 'initialize':\n"
            "   result={'protocolVersion':'2026-07-28','capabilities':{'tools':{}},'serverInfo':{'name':'PromptZero','version':'test'}}\n"
            "  elif req.get('method') == 'tools/list':\n"
            "   result={'tools':[{'name':'device_info','annotations':{'title':'device_info (low)','readOnlyHint':True,'destructiveHint':False}},{'name':'wifi_scan_ap','annotations':{'title':'wifi_scan_ap (medium)','readOnlyHint':False,'destructiveHint':False}}]}\n"
            "  elif req.get('method') == 'tools/call':\n"
            "   result={'content':[{'type':'text','text':'device health ok'}],'isError':False}\n"
            "  else: continue\n"
            "  if 'id' in req: print(json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}),flush=True)\n"
            "with open(os.environ['FAKE_ENV'], 'w') as f:\n"
            " json.dump({k:os.environ.get(k) for k in ('PROMPTZERO_MCP_ALLOW_HIGH','PROMPTZERO_MCP_ALLOW_CRITICAL','ANTHROPIC_API_KEY','OPENROUTER_API_KEY')},f)\n"
        )
        self.server.chmod(0o755)
        self.requests = self.root / "requests.jsonl"
        self.child_env = self.root / "child-env.json"
        self.report = self.root / "report.json"
        self.env = os.environ.copy()
        self.env.update(
            {
                "FAKE_REQUESTS": str(self.requests),
                "FAKE_ENV": str(self.child_env),
                "PROMPTZERO_MCP_ALLOW_HIGH": "1",
                "PROMPTZERO_MCP_ALLOW_CRITICAL": "1",
                "ANTHROPIC_API_KEY": "test-only-secret",
                "OPENROUTER_API_KEY": "test-only-secret",
            }
        )

    def run_client(self, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                sys.executable,
                str(CLIENT),
                "--command",
                str(self.server),
                "--port",
                "/dev/fake",
                "--report",
                str(self.report),
                *args,
            ],
            env=self.env,
            text=True,
            capture_output=True,
            timeout=5,
            check=False,
        )

    def test_smoke_initializes_lists_and_calls_only_selected_safe_tool(self) -> None:
        result = self.run_client()

        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads(self.report.read_text())
        self.assertEqual(report["status"], "ok")
        self.assertEqual(report["tool"], "device_info")
        self.assertEqual(report["raw_evidence"]["exit_status"], 0)
        self.assertIn("device health ok", json.dumps(report["raw_evidence"]))
        requests = [json.loads(line) for line in self.requests.read_text().splitlines()]
        self.assertEqual(
            [r["method"] for r in requests],
            ["initialize", "notifications/initialized", "tools/list", "tools/call"],
        )
        self.assertEqual(
            requests[-1]["params"], {"name": "device_info", "arguments": {}}
        )
        self.assertEqual(
            json.loads(self.child_env.read_text()),
            {
                "PROMPTZERO_MCP_ALLOW_HIGH": None,
                "PROMPTZERO_MCP_ALLOW_CRITICAL": None,
                "ANTHROPIC_API_KEY": None,
                "OPENROUTER_API_KEY": None,
            },
        )

    def test_explicit_wifi_smoke_uses_bounded_scan_duration(self) -> None:
        result = self.run_client("--tool", "wifi_scan_ap", "--duration", "2")

        self.assertEqual(result.returncode, 0, result.stderr)
        requests = [json.loads(line) for line in self.requests.read_text().splitlines()]
        self.assertEqual(
            requests[-1]["params"],
            {"name": "wifi_scan_ap", "arguments": {"duration_seconds": 2}},
        )

    def test_high_or_critical_annotation_prevents_the_tool_call(self) -> None:
        self.server.write_text(
            self.server.read_text().replace(
                "device_info (low)", "device_info (critical)"
            )
        )

        result = self.run_client()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("risk annotation", json.loads(self.report.read_text())["error"])
        requests = [json.loads(line) for line in self.requests.read_text().splitlines()]
        self.assertEqual(
            [r["method"] for r in requests],
            ["initialize", "notifications/initialized", "tools/list"],
        )
        self.assertEqual(json.loads(self.report.read_text())["status"], "error")

    def test_missing_risk_annotations_prevent_the_tool_call(self) -> None:
        content = self.server.read_text().replace(
            ",'annotations':{'title':'device_info (low)','readOnlyHint':True,'destructiveHint':False}",
            "",
        )
        self.server.write_text(content)

        result = self.run_client()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("risk annotations", json.loads(self.report.read_text())["error"])
        requests = [json.loads(line) for line in self.requests.read_text().splitlines()]
        self.assertEqual(
            [r["method"] for r in requests],
            ["initialize", "notifications/initialized", "tools/list"],
        )

    def test_initialize_timeout_is_bounded_and_saved_as_failure_evidence(self) -> None:
        silent = self.root / "silent-server"
        silent.write_text(
            "#!/usr/bin/python3\nimport sys,time\nfor _ in sys.stdin: time.sleep(10)\n"
        )
        silent.chmod(0o755)
        started = time.monotonic()

        result = subprocess.run(
            [
                sys.executable,
                str(CLIENT),
                "--command",
                str(silent),
                "--port",
                "/dev/fake",
                "--report",
                str(self.report),
                "--timeout",
                "0.2",
            ],
            env=self.env,
            text=True,
            capture_output=True,
            timeout=3,
            check=False,
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("timed out", json.loads(self.report.read_text())["error"])
        self.assertLess(time.monotonic() - started, 2)
        report = json.loads(self.report.read_text())
        self.assertEqual(report["status"], "error")
        self.assertIn("initialize", json.dumps(report["raw_evidence"]))

    def test_oversized_mcp_line_is_rejected_with_bounded_buffer(self) -> None:
        body = self.server.read_text().replace(
            "print(json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}),flush=True)",
            "print(json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}) + (' ' * 1100000),flush=True)",
        )
        self.server.write_text(body)
        started = time.monotonic()

        result = self.run_client()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("exceeded", json.loads(self.report.read_text())["error"])
        self.assertLess(time.monotonic() - started, 2)


if __name__ == "__main__":
    unittest.main()
