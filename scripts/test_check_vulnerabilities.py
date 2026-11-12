"""Boundary tests for the shared govulncheck policy gate."""

import contextlib
import importlib.util
import io
import json
import subprocess
import unittest
from pathlib import Path
from unittest import mock

SCRIPT = Path(__file__).with_name("check_vulnerabilities.py")
SPEC = importlib.util.spec_from_file_location("check_vulnerabilities", SCRIPT)
GATE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GATE)


def record(name, value):
    return json.dumps({name: value})


def config():
    return {
        "protocol_version": "v1.0.0",
        "scanner_name": "govulncheck",
        "scanner_version": "v1.9.0",
        "scan_level": "symbol",
        "scan_mode": "source",
    }


def report(*findings):
    messages = [record("config", config()), record("SBOM", {"modules": []})]
    for item in findings:
        messages.append(record("osv", {"id": item["osv"]}))
        messages.append(record("finding", item))
    return "\n".join(messages) + "\n"


def finding(osv="GO-2026-6911", module="github.com/mark3labs/mcp-go",
            version="v1.3.0", function="NewStreamableHTTP", fixed=None):
    trace = {"module": module, "version": version}
    if function:
        trace.update({
            "package": module + "/client/transport",
            "function": function,
            "position": {"filename": "client/transport/client.go", "offset": 1, "line": 1, "column": 1},
        })
    result = {"osv": osv, "trace": [trace]}
    if fixed is not None:
        result["fixed_version"] = fixed
    return result


def passing_test_events():
    names = [
        "TestFederationClientsDoNotFollowRedirects",
        "TestFederationClientsDoNotFollowRedirects/http",
        "TestFederationClientsDoNotFollowRedirects/sse",
    ]
    events = []
    for name in names:
        events.extend([
            {"Action": "run", "Test": name},
            {"Action": "output", "Test": name, "Output": "test started"},
            {"Action": "pass", "Test": name},
        ])
    return "\n".join(json.dumps(event) for event in events)


class GateBoundaryTests(unittest.TestCase):
    def invoke(self, scan_output, scan_code=0, test_output="", test_code=0):
        calls = []

        def run(command, **kwargs):
            calls.append(command)
            if command[0:3] == ["go", "run", "golang.org/x/vuln/cmd/govulncheck@v1.9.0"]:
                return subprocess.CompletedProcess(command, scan_code, scan_output, "scan stderr")
            return subprocess.CompletedProcess(command, test_code, test_output, "test stderr")

        stdout = io.StringIO()
        with mock.patch.object(GATE.subprocess, "run", side_effect=run), contextlib.redirect_stdout(stdout):
            result = GATE.main()
        return result, calls, stdout.getvalue()

    def test_clean_report_and_noncalled_findings_pass(self):
        informational = finding(function=None)
        result, calls, output = self.invoke(report(informational))
        self.assertEqual(result, 0)
        self.assertEqual(len(calls), 1)
        self.assertIn("GO-2026-6911", output)

        package_finding = finding(function=None)
        package_finding["trace"][0]["package"] = "github.com/mark3labs/mcp-go/client"
        result, calls, _ = self.invoke(report(package_finding))
        self.assertEqual(result, 0)
        self.assertEqual(len(calls), 1)

    def test_known_called_finding_passes_only_after_all_redirect_tests(self):
        result, calls, output = self.invoke(report(finding()), test_output=passing_test_events())
        self.assertEqual(result, 0)
        self.assertEqual(len(calls), 2)
        self.assertIn("GO-2026-6911", output)

    def test_other_called_id_fails(self):
        result, calls, _ = self.invoke(report(finding(osv="GO-2026-9000")))
        self.assertNotEqual(result, 0)
        self.assertEqual(len(calls), 1)

    def test_changed_module_fails(self):
        result, _, _ = self.invoke(report(finding(module="example.com/other")), test_output=passing_test_events())
        self.assertNotEqual(result, 0)

    def test_changed_version_fails(self):
        result, _, _ = self.invoke(report(finding(version="v1.3.1")), test_output=passing_test_events())
        self.assertNotEqual(result, 0)

    def test_published_fixed_version_fails(self):
        result, _, _ = self.invoke(report(finding(fixed="v1.4.0")), test_output=passing_test_events())
        self.assertNotEqual(result, 0)

    def test_malformed_truncated_and_empty_reports_fail(self):
        for scan_output in ("{\"config\":", "", record("config", config()) + "\n{"):
            with self.subTest(scan_output=scan_output):
                result, _, _ = self.invoke(scan_output)
                self.assertNotEqual(result, 0)

    def test_wrong_or_missing_config_fails(self):
        configs = [
            {**config(), "scan_level": "package"},
            {**config(), "scan_mode": "binary"},
            {key: value for key, value in config().items() if key != "scanner_name"},
        ]
        for bad_config in configs:
            with self.subTest(config=bad_config):
                result, _, _ = self.invoke(record("config", bad_config))
                self.assertNotEqual(result, 0)
        result, _, _ = self.invoke(record("SBOM", {"modules": []}))
        self.assertNotEqual(result, 0)

    def test_malformed_or_wrong_level_trace_fails(self):
        called_frame = finding()["trace"][0]
        malformed = [
            {"osv": "GO-2026-6911", "trace": []},
            {"osv": "GO-2026-6911", "trace": [{"module": "github.com/mark3labs/mcp-go"}]},
            {"osv": "GO-2026-6911", "trace": [{"module": "github.com/mark3labs/mcp-go", "version": "v1.3.0", "position": {}}]},
            {"osv": "GO-2026-6911", "trace": [{"module": "github.com/mark3labs/mcp-go", "version": "v1.3.0", "function": "NewStreamableHTTP"}]},
            {"osv": "GO-2026-6911", "trace": [{"module": "github.com/mark3labs/mcp-go", "version": "v1.3.0"}, called_frame]},
        ]
        for item in malformed:
            with self.subTest(finding=item):
                result, calls, _ = self.invoke(report(item), test_output=passing_test_events())
                self.assertNotEqual(result, 0)
                self.assertEqual(len(calls), 1)

    def test_scanner_failure_fails_even_with_clean_json(self):
        result, _, _ = self.invoke(report(), scan_code=2)
        self.assertNotEqual(result, 0)

    def test_missing_skipped_failed_or_nonzero_mitigation_test_fails(self):
        event_sets = [
            "",
            passing_test_events().replace('"Action": "pass", "Test": "TestFederationClientsDoNotFollowRedirects/sse"', '"Action": "skip", "Test": "TestFederationClientsDoNotFollowRedirects/sse"'),
            passing_test_events().replace('"Action": "pass", "Test": "TestFederationClientsDoNotFollowRedirects/http"', '"Action": "fail", "Test": "TestFederationClientsDoNotFollowRedirects/http"'),
        ]
        for test_output in event_sets:
            with self.subTest(test_output=test_output):
                result, _, _ = self.invoke(report(finding()), test_output=test_output)
                self.assertNotEqual(result, 0)
        result, _, _ = self.invoke(report(finding()), test_output=passing_test_events(), test_code=1)
        self.assertNotEqual(result, 0)


if __name__ == "__main__":
    unittest.main()
