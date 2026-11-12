#!/usr/bin/env python3
"""Run the pinned govulncheck scan and enforce the repository advisory policy."""

import json
import subprocess
import sys

SCANNER = ["go", "run", "golang.org/x/vuln/cmd/govulncheck@v1.9.0", "-json", "./..."]
MITIGATION_TEST = [
    "go", "test", "-json", "-race", "-count=1", "./internal/mcpfed",
    "-run", "^TestFederationClientsDoNotFollowRedirects$",
]
ALLOWED_ID = "GO-2026-6911"
ALLOWED_MODULE = "github.com/mark3labs/mcp-go"
ALLOWED_VERSION = "v1.3.0"
REQUIRED_TESTS = {
    "TestFederationClientsDoNotFollowRedirects",
    "TestFederationClientsDoNotFollowRedirects/http",
    "TestFederationClientsDoNotFollowRedirects/sse",
}


class ReportError(ValueError):
    """The scanner report is invalid or incomplete."""


def fail(message):
    print(f"vulnerability gate: {message}", file=sys.stderr)
    return 1


def parse_messages(raw):
    decoder = json.JSONDecoder()
    messages = []
    offset = 0
    while offset < len(raw):
        while offset < len(raw) and raw[offset].isspace():
            offset += 1
        if offset == len(raw):
            break
        try:
            message, offset = decoder.raw_decode(raw, offset)
        except json.JSONDecodeError as error:
            raise ReportError(f"malformed or truncated JSON at byte {error.pos}") from error
        if not isinstance(message, dict) or len(message) != 1:
            raise ReportError("each stream message must contain exactly one object field")
        messages.append(message)
    if not messages:
        raise ReportError("scanner produced an empty report")
    return messages


def validate_config(message):
    config = message.get("config")
    required = {
        "protocol_version": "v1.0.0",
        "scanner_name": "govulncheck",
        "scanner_version": "v1.9.0",
        "scan_level": "symbol",
        "scan_mode": "source",
    }
    if not isinstance(config, dict) or any(config.get(key) != value for key, value in required.items()):
        raise ReportError("missing or unsupported govulncheck symbol/source configuration")


def validate_sbom(sbom):
    if not isinstance(sbom, dict) or not isinstance(sbom.get("modules"), list):
        raise ReportError("missing or malformed SBOM")
    for module in sbom["modules"]:
        if not isinstance(module, dict) or not isinstance(module.get("path"), str):
            raise ReportError("malformed SBOM module")
        if "version" in module and not isinstance(module["version"], str):
            raise ReportError("malformed SBOM module version")


def validate_finding(finding):
    if not isinstance(finding, dict) or not isinstance(finding.get("osv"), str) or not finding["osv"]:
        raise ReportError("finding has no vulnerability ID")
    if "fixed_version" in finding and not isinstance(finding["fixed_version"], str):
        raise ReportError(f"{finding['osv']} has malformed fixed_version")
    trace = finding.get("trace")
    if not isinstance(trace, list) or not trace:
        raise ReportError(f"{finding['osv']} has no trace")
    for frame in trace:
        if not isinstance(frame, dict) or not isinstance(frame.get("module"), str) or not frame["module"]:
            raise ReportError(f"{finding['osv']} has a malformed trace frame")
        for field in ("version", "package", "function", "receiver"):
            if field in frame and not isinstance(frame[field], str):
                raise ReportError(f"{finding['osv']} has a malformed trace {field}")
        if "position" in frame:
            position = frame["position"]
            if not isinstance(position, dict) or not all(
                isinstance(position.get(field), int) for field in ("offset", "line", "column")
            ):
                raise ReportError(f"{finding['osv']} has a malformed trace position")
            if not isinstance(position.get("filename"), str):
                raise ReportError(f"{finding['osv']} has a malformed trace filename")
        if frame.get("function"):
            position = frame.get("position")
            if not frame.get("package") or not isinstance(position, dict) or not position.get("filename"):
                raise ReportError(f"{finding['osv']} has an incomplete symbol-level source trace")
            if position["offset"] < 0 or position["line"] < 1 or position["column"] < 1:
                raise ReportError(f"{finding['osv']} has an invalid source position")
        elif "position" in frame:
            raise ReportError(f"{finding['osv']} has a position without a called symbol")
    if not isinstance(trace[0].get("version"), str) or not trace[0]["version"]:
        raise ReportError(f"{finding['osv']} has no affected module version")
    symbol_frames = [bool(frame.get("function")) for frame in trace]
    if any(symbol_frames) and not all(symbol_frames):
        raise ReportError(f"{finding['osv']} mixes symbol and non-symbol trace levels")
    if not any(symbol_frames) and len(trace) != 1:
        raise ReportError(f"{finding['osv']} has a multi-frame non-symbol trace")
    return all(symbol_frames)


def validate_report(raw):
    messages = parse_messages(raw)
    if set(messages[0]) != {"config"}:
        raise ReportError("config must be the first stream message")
    validate_config(messages[0])
    sboms = []
    osv_ids = set()
    findings = []
    for message in messages[1:]:
        kind, value = next(iter(message.items()))
        if kind == "SBOM":
            validate_sbom(value)
            sboms.append(value)
        elif kind == "osv":
            if not isinstance(value, dict) or not isinstance(value.get("id"), str) or not value["id"]:
                raise ReportError("malformed OSV entry")
            osv_ids.add(value["id"])
        elif kind == "finding":
            called = validate_finding(value)
            findings.append((value, called))
        elif kind == "progress":
            if not isinstance(value, dict) or not isinstance(value.get("message"), str):
                raise ReportError("malformed progress message")
        else:
            raise ReportError(f"unknown scanner message type: {kind}")
    if len(sboms) != 1:
        raise ReportError("report must contain exactly one SBOM")
    for finding, _ in findings:
        if finding["osv"] not in osv_ids:
            raise ReportError(f"finding {finding['osv']} has no matching OSV entry")
    return findings


def is_allowed_finding(finding):
    if finding["osv"] != ALLOWED_ID or finding.get("fixed_version", ""):
        return False
    affected_module = finding["trace"][0]
    return affected_module["module"] == ALLOWED_MODULE and affected_module["version"] == ALLOWED_VERSION


def passing_mitigation_events(raw):
    seen = set()
    try:
        for line in raw.splitlines():
            if not line.strip():
                continue
            event = json.loads(line)
            if not isinstance(event, dict):
                return False
            if event.get("Test") in REQUIRED_TESTS:
                action = event.get("Action")
                if action == "pass":
                    seen.add(event["Test"])
                elif action in ("fail", "skip") or action not in ("run", "output"):
                    return False
    except (json.JSONDecodeError, TypeError):
        return False
    return seen == REQUIRED_TESTS


def run(command):
    return subprocess.run(command, text=True, capture_output=True, check=False)


def main():
    try:
        scan = run(SCANNER)
    except OSError as error:
        return fail(f"could not start govulncheck: {error}")
    raw_report = scan.stdout or ""
    if raw_report:
        print(raw_report, end="" if raw_report.endswith("\n") else "\n")
    if scan.returncode != 0:
        return fail(f"govulncheck exited {scan.returncode}: {scan.stderr.strip()}")
    try:
        findings = validate_report(raw_report)
    except ReportError as error:
        return fail(str(error))
    called_findings = [finding for finding, called in findings if called]
    if not called_findings:
        print("vulnerability gate: no called vulnerability findings")
        return 0
    if any(not is_allowed_finding(finding) for finding in called_findings):
        ids = ", ".join(sorted({finding["osv"] for finding in called_findings}))
        return fail(f"unapproved called vulnerability finding(s): {ids}")
    try:
        tests = run(MITIGATION_TEST)
    except OSError as error:
        return fail(f"could not start redirect mitigation tests: {error}")
    if tests.returncode != 0:
        return fail(f"redirect mitigation tests exited {tests.returncode}: {tests.stderr.strip()}")
    if not passing_mitigation_events(tests.stdout):
        return fail("redirect mitigation parent, http, and sse tests did not all pass")
    print("vulnerability gate: documented redirect mitigation verified")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
