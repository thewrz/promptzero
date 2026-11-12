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
            "   if os.environ.get('FAKE_LARGE_CATALOG') == '1':\n"
            "    details='Representative input documentation for request validation, safe hardware access, output fields, and timing limits. ' * 8\n"
            "    properties={f'field_{i}':{'type':'string','description':details} for i in range(8)}\n"
            "    tools=[{'name':f'catalog_tool_{i:03d}','description':details,'inputSchema':{'type':'object','properties':properties},'annotations':{'title':f'catalog_tool_{i:03d} (low)','readOnlyHint':True,'destructiveHint':False}} for i in range(706)]\n"
            "    tools.extend([{'name':'device_info','description':'Get device status.','inputSchema':{'type':'object','properties':{}},'annotations':{'title':'device_info (low)','readOnlyHint':True,'destructiveHint':False}},{'name':'wifi_scan_ap','description':'Scan nearby access points.','inputSchema':{'type':'object','properties':{}},'annotations':{'title':'wifi_scan_ap (medium)','readOnlyHint':False,'destructiveHint':False}}])\n"
            "    result={'tools':tools}\n"
            "   else:\n"
            "    result={'tools':[{'name':'device_info','annotations':{'title':'device_info (low)','readOnlyHint':True,'destructiveHint':False}},{'name':'wifi_scan_ap','annotations':{'title':'wifi_scan_ap (medium)','readOnlyHint':False,'destructiveHint':False}}]}\n"
            "  elif req.get('method') == 'tools/call':\n"
            "   result={'content':[{'type':'text','text':'device health ok'}],'isError':False}\n"
            "  else: continue\n"
            "  if 'id' in req: print(json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}),flush=True)\n"
            "with open(os.environ['FAKE_ENV'], 'w') as f:\n"
            " json.dump({'env':{k:os.environ.get(k) for k in ('FLIPPER_PORT','PROMPTZERO_MCP_ALLOW_HIGH','PROMPTZERO_MCP_ALLOW_CRITICAL','ANTHROPIC_API_KEY','OPENROUTER_API_KEY')},'argv':sys.argv[1:]},f)\n"
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
                "FLIPPER_PORT": "/dev/wrong",
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
        self.assertEqual(
            json.loads(self.child_env.read_text())["argv"],
            ["--mcp", "--no-wifi", "--port", "/dev/fake"],
        )
        requests = [json.loads(line) for line in self.requests.read_text().splitlines()]
        self.assertEqual(
            [r["method"] for r in requests],
            ["initialize", "notifications/initialized", "tools/list", "tools/call"],
        )
        self.assertEqual(
            requests[-1]["params"], {"name": "device_info", "arguments": {}}
        )
        self.assertEqual(
            json.loads(self.child_env.read_text())["env"],
            {
                "FLIPPER_PORT": "/dev/fake",
                "PROMPTZERO_MCP_ALLOW_HIGH": None,
                "PROMPTZERO_MCP_ALLOW_CRITICAL": None,
                "ANTHROPIC_API_KEY": None,
                "OPENROUTER_API_KEY": None,
            },
        )

    def test_explicit_wifi_smoke_uses_bounded_scan_duration(self) -> None:
        result = self.run_client(
            "--tool", "wifi_scan_ap", "--mode", "wifi", "--duration", "2"
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        requests = [json.loads(line) for line in self.requests.read_text().splitlines()]
        self.assertEqual(
            requests[-1]["params"],
            {"name": "wifi_scan_ap", "arguments": {"duration_seconds": 2}},
        )
        self.assertEqual(
            json.loads(self.child_env.read_text())["argv"],
            ["--mcp", "--wifi", "--port", "/dev/fake"],
        )

    def test_wifi_bridge_command_and_dtr_options_are_forwarded(self) -> None:
        result = self.run_client(
            "--tool",
            "wifi_scan_ap",
            "--mode",
            "wifi",
            "--bridge-command",
            "loader open test-bridge",
            "--bridge-exit-dtr",
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            json.loads(self.child_env.read_text())["argv"],
            [
                "--mcp",
                "--wifi",
                "--marauder-bridge",
                "--marauder-bridge-command",
                "loader open test-bridge",
                "--marauder-bridge-exit-dtr",
                "--port",
                "/dev/fake",
            ],
        )

    def test_catalog_over_one_mib_still_lists_and_calls_device_info(self) -> None:
        self.env["FAKE_LARGE_CATALOG"] = "1"

        result = self.run_client()

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        report = json.loads(self.report.read_text())
        list_response = next(
            line
            for line in report["raw_evidence"]["responses"]
            if json.loads(line).get("id") == 2
        )
        self.assertGreater(len(list_response.encode()), 1_048_576)
        self.assertEqual(
            report["raw_evidence"]["requests"][-1]["params"]["name"], "device_info"
        )
        self.assertEqual(report["status"], "ok")

    def test_bridge_options_are_rejected_when_wifi_is_off(self) -> None:
        result = self.run_client("--bridge-exit-dtr")

        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.report.exists())

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
            "print(json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}) + (' ' * 8388609),flush=True)",
        )
        self.server.write_text(body)
        started = time.monotonic()

        result = self.run_client()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("exceeded", json.loads(self.report.read_text())["error"])
        self.assertLess(time.monotonic() - started, 2)

    def test_child_gets_shutdown_grace_to_finish_cleanup(self) -> None:
        graceful = self.root / "graceful-server"
        graceful.write_text(
            "#!/usr/bin/python3\n"
            "import json, os, signal, sys, time\n"
            "def stop(*_):\n"
            " time.sleep(0.7)\n"
            " with open(os.environ['CLEANUP_MARKER'],'w') as f: f.write('done')\n"
            " sys.exit(0)\n"
            "signal.signal(signal.SIGTERM, stop)\n"
            "for line in sys.stdin:\n"
            " req=json.loads(line); method=req.get('method')\n"
            " if method == 'initialize': result={'protocolVersion':'2026-07-28','capabilities':{},'serverInfo':{'name':'PromptZero','version':'test'}}\n"
            " elif method == 'tools/list': result={'tools':[{'name':'device_info','annotations':{'title':'device_info (low)','destructiveHint':False}}]}\n"
            " elif method == 'tools/call': result={'content':[{'type':'text','text':'ok'}],'isError':False}\n"
            " else: continue\n"
            " if 'id' in req: print(json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}),flush=True)\n"
            "while True: time.sleep(10)\n"
        )
        graceful.chmod(0o755)
        env = self.env.copy()
        marker = self.root / "cleanup-complete"
        env["CLEANUP_MARKER"] = str(marker)

        result = subprocess.run(
            [
                sys.executable,
                str(CLIENT),
                "--command",
                str(graceful),
                "--port",
                "/dev/fake",
                "--report",
                str(self.report),
                "--timeout",
                "0.2",
            ],
            env=env,
            text=True,
            capture_output=True,
            timeout=3,
            check=False,
        )

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(marker.read_text(), "done")
        self.assertEqual(
            json.loads(self.report.read_text())["raw_evidence"]["exit_status"], 0
        )


if __name__ == "__main__":
    unittest.main()
