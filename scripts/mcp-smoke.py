#!/usr/bin/env python3
"""Run a bounded MCP initialize/list/safe-call smoke check and save evidence."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import pathlib
import selectors
import signal
import subprocess
import sys
import threading
import time
from typing import Any

PROTOCOL_VERSION = "2026-07-28"
MAX_LINE_BYTES = 1_048_576
STDERR_LIMIT_BYTES = 32_768
ALLOWED_TOOLS = {"device_info", "wifi_scan_ap"}


class SmokeError(Exception):
    """An MCP startup, protocol, policy, or call failure."""


class MCPProcess:
    """A bounded JSON-RPC stdio client for one PromptZero child process."""

    def __init__(self, command: list[str], env: dict[str, str], timeout: float) -> None:
        self.timeout = timeout
        self.process = subprocess.Popen(
            command,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            bufsize=0,
            env=env,
            start_new_session=True,
        )
        assert self.process.stdin is not None
        assert self.process.stdout is not None
        assert self.process.stderr is not None
        self.stdin = self.process.stdin
        self.stdout = self.process.stdout
        self.stderr = self.process.stderr
        os.set_blocking(self.stdout.fileno(), False)
        self.selector = selectors.DefaultSelector()
        self.selector.register(self.stdout, selectors.EVENT_READ)
        self.buffer = bytearray()
        self.requests: list[dict[str, Any]] = []
        self.responses: list[str] = []
        self.stderr_tail = bytearray()
        self.stderr_lock = threading.Lock()
        self.stderr_thread = threading.Thread(target=self._capture_stderr, daemon=True)
        self.stderr_thread.start()

    def request(
        self, request_id: int, method: str, params: dict[str, Any]
    ) -> dict[str, Any]:
        request = {
            "jsonrpc": "2.0",
            "id": request_id,
            "method": method,
            "params": params,
        }
        self._write(request)
        deadline = time.monotonic() + self.timeout
        while True:
            response = self._read_message(deadline)
            if response.get("id") != request_id:
                continue
            if "error" in response:
                raise SmokeError(f"MCP {method} failed: {response['error']}")
            result = response.get("result")
            if not isinstance(result, dict):
                raise SmokeError(f"MCP {method} returned no result object")
            return result

    def notify(self, method: str, params: dict[str, Any]) -> None:
        self._write({"jsonrpc": "2.0", "method": method, "params": params})

    def _write(self, message: dict[str, Any]) -> None:
        self.requests.append(message)
        try:
            self.stdin.write(
                (json.dumps(message, separators=(",", ":")) + "\n").encode()
            )
            self.stdin.flush()
        except (BrokenPipeError, OSError) as exc:
            raise SmokeError(
                f"MCP child stopped while sending {message['method']}: {exc}"
            ) from exc

    def _read_message(self, deadline: float) -> dict[str, Any]:
        while True:
            newline = self.buffer.find(b"\n")
            if newline >= 0:
                if newline > MAX_LINE_BYTES:
                    raise SmokeError(f"MCP stdout line exceeded {MAX_LINE_BYTES} bytes")
                line = bytes(self.buffer[:newline])
                del self.buffer[: newline + 1]
                self.responses.append(line.decode("utf-8", errors="replace"))
                try:
                    message = json.loads(line)
                except (UnicodeDecodeError, json.JSONDecodeError) as exc:
                    raise SmokeError(
                        f"MCP stdout contained invalid JSON: {exc}"
                    ) from exc
                if not isinstance(message, dict):
                    raise SmokeError("MCP stdout message was not a JSON object")
                return message
            if len(self.buffer) > MAX_LINE_BYTES:
                raise SmokeError(f"MCP stdout line exceeded {MAX_LINE_BYTES} bytes")
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise SmokeError("timed out waiting for MCP response")
            if not self.selector.select(remaining):
                raise SmokeError("timed out waiting for MCP response")
            chunk = os.read(self.stdout.fileno(), 65_536)
            if not chunk:
                status = self.process.poll()
                raise SmokeError(
                    f"MCP child closed stdout before responding (exit status {status})"
                )
            self.buffer.extend(chunk)

    def _capture_stderr(self) -> None:
        while chunk := self.stderr.read(4096):
            with self.stderr_lock:
                self.stderr_tail.extend(chunk)
                if len(self.stderr_tail) > STDERR_LIMIT_BYTES:
                    del self.stderr_tail[: len(self.stderr_tail) - STDERR_LIMIT_BYTES]

    def close(self) -> None:
        try:
            self.stdin.close()
        except OSError:
            pass
        if self.process.poll() is None:
            try:
                self.process.wait(timeout=min(1.0, self.timeout))
            except subprocess.TimeoutExpired:
                self._signal_group(signal.SIGTERM)
                try:
                    self.process.wait(timeout=0.5)
                except subprocess.TimeoutExpired:
                    self._signal_group(signal.SIGKILL)
                    self.process.wait(timeout=1)
        self.selector.close()
        self.stdout.close()
        self.stderr_thread.join(timeout=0.5)
        self.stderr.close()

    def _signal_group(self, signum: int) -> None:
        try:
            os.killpg(self.process.pid, signum)
        except ProcessLookupError:
            pass

    def evidence(self) -> dict[str, Any]:
        with self.stderr_lock:
            stderr = self.stderr_tail.decode("utf-8", errors="replace")
        return {
            "requests": self.requests,
            "responses": self.responses,
            "stderr": stderr,
            "exit_status": self.process.returncode,
        }


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--port", required=True, help="Flipper serial device passed to the launcher"
    )
    parser.add_argument(
        "--command",
        default=str(
            pathlib.Path(__file__).resolve().parents[1]
            / "contrib/linux/promptzero-mcp-wrapper"
        ),
        help="launcher or PromptZero executable (default: repository Linux wrapper)",
    )
    parser.add_argument("--tool", choices=sorted(ALLOWED_TOOLS), default="device_info")
    parser.add_argument(
        "--duration", type=int, default=3, help="wifi_scan_ap duration, 1–15 seconds"
    )
    parser.add_argument(
        "--timeout", type=float, default=20, help="per-request deadline in seconds"
    )
    parser.add_argument(
        "--report",
        type=pathlib.Path,
        help="new local JSON evidence file (never overwritten)",
    )
    args = parser.parse_args(argv)
    if not args.port.strip():
        parser.error("--port cannot be empty")
    if not 1 <= args.duration <= 15:
        parser.error("--duration must be between 1 and 15 seconds")
    if not 0.1 <= args.timeout <= 300:
        parser.error("--timeout must be between 0.1 and 300 seconds")
    return args


def call_selected_tool(
    client: MCPProcess, tool_name: str, duration: int
) -> dict[str, Any]:
    initialized = client.request(
        1,
        "initialize",
        {
            "protocolVersion": PROTOCOL_VERSION,
            "capabilities": {},
            "clientInfo": {"name": "promptzero-mcp-smoke", "version": "1"},
        },
    )
    if not initialized.get("protocolVersion") or not isinstance(
        initialized.get("serverInfo"), dict
    ):
        raise SmokeError(
            "MCP initialize response omitted protocolVersion or serverInfo"
        )
    client.notify("notifications/initialized", {})
    catalog = client.request(2, "tools/list", {})
    tools = catalog.get("tools")
    if not isinstance(tools, list):
        raise SmokeError("MCP tools/list response omitted its tools array")
    selected = next(
        (
            item
            for item in tools
            if isinstance(item, dict) and item.get("name") == tool_name
        ),
        None,
    )
    if selected is None:
        raise SmokeError(f"MCP tool {tool_name!r} is not available")
    ensure_allowed_annotations(tool_name, selected)
    arguments = {"duration_seconds": duration} if tool_name == "wifi_scan_ap" else {}
    result = client.request(
        3, "tools/call", {"name": tool_name, "arguments": arguments}
    )
    if result.get("isError") is True:
        raise SmokeError(f"MCP tool {tool_name!r} returned an error result")
    return result


def ensure_allowed_annotations(tool_name: str, tool: dict[str, Any]) -> None:
    annotations = tool.get("annotations")
    if not isinstance(annotations, dict):
        raise SmokeError(
            f"refusing MCP tool {tool_name!r}: risk annotations are missing"
        )
    title = str(annotations.get("title", "")).lower()
    risk = title.rsplit("(", 1)[-1].removesuffix(")").strip()
    if annotations.get("destructiveHint") is not False or risk not in {"low", "medium"}:
        raise SmokeError(
            f"refusing MCP tool {tool_name!r}: risk annotations do not confirm low or medium risk"
        )


def report_path(requested: pathlib.Path | None) -> pathlib.Path:
    if requested is not None:
        return requested
    stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    return pathlib.Path.cwd() / f"promptzero-mcp-smoke-{stamp}.json"


def save_report(path: pathlib.Path, report: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    fd = os.open(path, flags, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as output:
        json.dump(report, output, indent=2, sort_keys=True)
        output.write("\n")


def run(argv: list[str] | None = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    started = dt.datetime.now(dt.timezone.utc)
    evidence: dict[str, Any] = {"requests": [], "responses": [], "stderr": ""}
    report: dict[str, Any] = {
        "schema_version": 1,
        "started_at": started.isoformat(),
        "command": [args.command, "--mcp", "--port", args.port],
        "port": args.port,
        "tool": args.tool,
        "status": "error",
        "raw_evidence": evidence,
    }
    child_env = os.environ.copy()
    for key in (
        "PROMPTZERO_MCP_ALLOW_HIGH",
        "PROMPTZERO_MCP_ALLOW_CRITICAL",
        "ANTHROPIC_API_KEY",
        "OPENROUTER_API_KEY",
    ):
        child_env.pop(key, None)
    client: MCPProcess | None = None
    error: str | None = None
    try:
        client = MCPProcess(
            [args.command, "--mcp", "--port", args.port], child_env, args.timeout
        )
        result = call_selected_tool(client, args.tool, args.duration)
        report["result"] = result
        report["status"] = "ok"
    except (OSError, SmokeError, ValueError) as exc:
        error = str(exc)
        report["error"] = error
    finally:
        if client is not None:
            client.close()
            evidence.update(client.evidence())
            if report["status"] == "ok" and client.process.returncode != 0:
                error = f"MCP child exited with status {client.process.returncode} after the call"
                report["error"] = error
                report["status"] = "error"
        report["finished_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
    destination = report_path(args.report)
    try:
        save_report(destination, report)
    except OSError as exc:
        print(
            f"could not save MCP smoke evidence to {destination}: {exc}",
            file=sys.stderr,
        )
        return 1
    summary = {
        "status": report["status"],
        "tool": args.tool,
        "report": str(destination),
    }
    if error is not None:
        summary["error"] = error
    print(json.dumps(summary, sort_keys=True))
    return 0 if error is None else 1


if __name__ == "__main__":
    raise SystemExit(run())
