"""Process-level tests for the Linux PromptZero MCP launcher."""

from __future__ import annotations

import os
import pathlib
import pty
import shutil
import signal
import subprocess
import tempfile
import time
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
WRAPPER = ROOT / "contrib/linux/promptzero-mcp-wrapper"


class Device:
    def __init__(self) -> None:
        self.master, slave = pty.openpty()
        self.path = pathlib.Path(os.ttyname(slave))
        os.close(slave)

    def close(self) -> None:
        os.close(self.master)


class WrapperTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        self.device = Device()
        self.addCleanup(self.device.close)
        self.addCleanup(self.temp.cleanup)
        self.runtime = self.root / "runtime"
        self.runtime.mkdir()
        self.child_record = self.root / "child.json"
        self.child = self.root / "promptzero"
        self.child.write_text(
            "#!/usr/bin/python3\n"
            "import json, os, signal, sys, time\n"
            "with open(os.environ['CHILD_RECORD'], 'w') as f:\n"
            " json.dump(sys.argv[1:], f)\n"
            " f.flush()\n"
            "print('child-stdout', flush=True)\n"
            "if os.environ.get('CHILD_HOLD') == '1':\n"
            " signal.signal(signal.SIGTERM, lambda *_: sys.exit(143))\n"
            " while True: time.sleep(0.05)\n"
            "sys.exit(int(os.environ.get('CHILD_EXIT', '0')))\n"
        )
        self.child.chmod(0o755)
        self.env = os.environ.copy()
        self.env.update(
            {
                "FLIPPER_PORT": str(self.device.path),
                "PROMPTZERO_BIN": str(self.child),
                "PROMPTZERO_READY_ATTEMPTS": "1",
                "XDG_RUNTIME_DIR": str(self.runtime),
                "CHILD_RECORD": str(self.child_record),
            }
        )
        self.env.pop("PROMPTZERO_MCP_ALLOW_HIGH", None)
        self.env.pop("PROMPTZERO_MCP_ALLOW_CRITICAL", None)

    def run_wrapper(
        self, *args: str, env: dict[str, str] | None = None
    ) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [str(WRAPPER), *args],
            env=env or self.env,
            text=True,
            capture_output=True,
            timeout=5,
            check=False,
        )

    def test_missing_flipper_port_fails_on_stderr(self) -> None:
        env = self.env.copy()
        env.pop("FLIPPER_PORT")

        result = self.run_wrapper("--mcp", env=env)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("FLIPPER_PORT", result.stderr)
        self.assertEqual(result.stdout, "")

    def test_absent_port_fails_after_bounded_readiness(self) -> None:
        env = self.env.copy()
        env["FLIPPER_PORT"] = str(self.root / "appears-later")
        started = time.monotonic()

        result = self.run_wrapper("--mcp", env=env)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not present", result.stderr)
        self.assertEqual(result.stdout, "")
        self.assertLess(time.monotonic() - started, 2)

    def test_regular_file_is_rejected_as_not_a_character_device(self) -> None:
        regular = self.root / "not-a-device"
        regular.write_text("not serial")
        env = self.env.copy()
        env["FLIPPER_PORT"] = str(regular)

        result = self.run_wrapper("--mcp", env=env)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("character device", result.stderr)
        self.assertEqual(result.stdout, "")

    def test_missing_executable_fails_before_starting_mcp(self) -> None:
        env = self.env.copy()
        env["PROMPTZERO_BIN"] = str(self.root / "missing-promptzero")

        result = self.run_wrapper("--mcp", env=env)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("executable", result.stderr)
        self.assertEqual(result.stdout, "")

    def test_missing_stty_dependency_has_actionable_error(self) -> None:
        isolated = self.root / "path"
        isolated.mkdir()
        for command in (
            "id",
            "mkdir",
            "chmod",
            "readlink",
            "stat",
            "cksum",
            "flock",
            "sleep",
        ):
            source = shutil.which(command)
            if source:
                (isolated / command).symlink_to(source)
        env = self.env.copy()
        env["PATH"] = str(isolated)

        result = self.run_wrapper("--mcp", env=env)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("stty", result.stderr)
        self.assertEqual(result.stdout, "")

    def test_stty_failure_is_reported_without_starting_child(self) -> None:
        fake_bin = self.root / "bin"
        fake_bin.mkdir()
        failing_stty = fake_bin / "stty"
        failing_stty.write_text(
            "#!/bin/sh\nprintf 'stty rejected device\\n' >&2\nexit 1\n"
        )
        failing_stty.chmod(0o755)
        env = self.env.copy()
        env["PATH"] = f"{fake_bin}:{os.environ['PATH']}"

        result = self.run_wrapper("--mcp", env=env)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("stty rejected device", result.stderr)
        self.assertIn("raw mode", result.stderr)
        self.assertFalse(self.child_record.exists())
        self.assertEqual(result.stdout, "")

    def test_explicit_port_must_resolve_to_prepared_device(self) -> None:
        other = Device()
        self.addCleanup(other.close)

        result = self.run_wrapper("--mcp", "--port", str(other.path))

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must match FLIPPER_PORT", result.stderr)
        self.assertFalse(self.child_record.exists())

    def test_every_repeated_port_override_must_match_prepared_device(self) -> None:
        other = Device()
        self.addCleanup(other.close)

        result = self.run_wrapper(
            "--mcp", "--port", str(other.path), "--port", str(self.device.path)
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must match FLIPPER_PORT", result.stderr)
        self.assertFalse(self.child_record.exists())

    def test_transport_override_is_rejected(self) -> None:
        result = self.run_wrapper("--mcp", "--transport", "serial:///dev/other")

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--transport", result.stderr)
        self.assertFalse(self.child_record.exists())

    def test_symlink_aliases_share_the_same_lifetime_lock(self) -> None:
        alias = self.root / "flipper-alias"
        alias.symlink_to(self.device.path)
        env = self.env.copy()
        env["FLIPPER_PORT"] = str(alias)
        env["CHILD_HOLD"] = "1"
        first = subprocess.Popen(
            [str(WRAPPER), "--mcp"],
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        self.addCleanup(self._stop, first)
        self._wait_for(self.child_record)

        result = self.run_wrapper("--mcp", env=self.env)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("already in use", result.stderr)
        self.assertIn(str(first.pid), result.stderr)
        self.assertEqual(result.stdout, "")

    def test_by_id_alias_retarget_keeps_lock_and_child_stable(self) -> None:
        by_id = self.root / "dev" / "serial" / "by-id"
        by_id.mkdir(parents=True)
        stable_alias = by_id / "usb-Flipper-test"
        stable_alias.symlink_to(self.device.path)
        env = self.env.copy()
        env["FLIPPER_PORT"] = str(stable_alias)
        env["CHILD_HOLD"] = "1"
        first = subprocess.Popen(
            [str(WRAPPER), "--mcp"],
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        self.addCleanup(self._stop, first)
        self._wait_for(self.child_record)
        first_args = __import__("json").loads(self.child_record.read_text())
        self.assertEqual(first_args[-1], str(stable_alias))

        replacement = by_id / "replacement"
        replacement.symlink_to(self.root / "other-pty")
        other = Device()
        self.addCleanup(other.close)
        replacement.unlink()
        replacement.symlink_to(other.path)
        replacement.replace(stable_alias)

        second_env = env.copy()
        second_env.pop("CHILD_HOLD")
        result = self.run_wrapper("--mcp", env=second_env)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("already in use", result.stderr)
        self.assertIn(str(first.pid), result.stderr)
        self.assertEqual(result.stdout, "")

    def test_open_unrelated_serial_device_does_not_block_selected_device(self) -> None:
        unrelated = Device()
        self.addCleanup(unrelated.close)
        unrelated_slave = os.open(unrelated.path, os.O_RDWR | os.O_NOCTTY)
        self.addCleanup(os.close, unrelated_slave)

        result = self.run_wrapper("--mcp")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "child-stdout\n")

    def test_external_owner_of_selected_device_is_reported(self) -> None:
        slave = os.open(self.device.path, os.O_RDWR | os.O_NOCTTY)
        self.addCleanup(os.close, slave)

        result = self.run_wrapper("--mcp")

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("already open by PID", result.stderr)
        self.assertFalse(self.child_record.exists())
        self.assertEqual(result.stdout, "")

    def test_unrelated_device_can_launch_while_first_device_is_owned(self) -> None:
        other = Device()
        self.addCleanup(other.close)
        first_env = self.env.copy()
        first_env["CHILD_HOLD"] = "1"
        first = subprocess.Popen(
            [str(WRAPPER), "--mcp"],
            env=first_env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        self.addCleanup(self._stop, first)
        self._wait_for(self.child_record)
        other_env = self.env.copy()
        other_env["FLIPPER_PORT"] = str(other.path)
        other_env["CHILD_RECORD"] = str(self.root / "other-child.json")

        result = self.run_wrapper("--mcp", env=other_env)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("child-stdout", result.stdout)

    def test_child_arguments_stdout_and_exit_status_are_preserved(self) -> None:
        self.env["CHILD_EXIT"] = "23"

        result = self.run_wrapper("--mcp", "--read-only")

        self.assertEqual(result.returncode, 23)
        self.assertEqual(result.stdout, "child-stdout\n")
        args = __import__("json").loads(self.child_record.read_text())
        self.assertEqual(
            args, ["--mcp", "--read-only", "--port", str(self.device.path)]
        )

    def test_signal_reaches_child_and_becomes_child_exit_status(self) -> None:
        env = self.env.copy()
        env["CHILD_HOLD"] = "1"
        process = subprocess.Popen(
            [str(WRAPPER), "--mcp"],
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        self._wait_for(self.child_record)

        process.send_signal(signal.SIGTERM)
        _, stderr = process.communicate(timeout=3)

        self.assertEqual(process.returncode, 143, stderr)

    def _wait_for(self, path: pathlib.Path) -> None:
        deadline = time.monotonic() + 2
        while time.monotonic() < deadline:
            if path.exists():
                return
            time.sleep(0.01)
        self.fail(f"child did not create {path.name}")

    @staticmethod
    def _stop(process: subprocess.Popen[str]) -> None:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
            try:
                process.communicate(timeout=2)
            except subprocess.TimeoutExpired:
                process.kill()
                process.communicate()


if __name__ == "__main__":
    unittest.main()
