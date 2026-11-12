"""scripts/lint.sh, observed through a stand-in linter on PATH."""

import os
import pathlib
import stat
import subprocess
import tempfile
import unittest

REPO_ROOT = pathlib.Path(__file__).resolve().parent.parent
LINT_SCRIPT = REPO_ROOT / "scripts" / "lint.sh"

# Records what the script hands the linter, then exits with the status the test asked for.
STAND_IN = """#!/bin/sh
{
  printf 'cache=%s\\n' "$GOLANGCI_LINT_CACHE"
  printf 'toolchain=%s\\n' "$GOTOOLCHAIN"
  printf 'cwd=%s\\n' "$PWD"
  printf 'args=%s\\n' "$*"
} > "$LINT_PROBE"
exit "${LINT_STATUS:-0}"
"""


def module_toolchain():
    for line in (REPO_ROOT / "go.mod").read_text(encoding="utf-8").splitlines():
        if line.startswith("toolchain "):
            return line.split()[1]
    raise AssertionError("go.mod names no toolchain")


class LintScriptTest(unittest.TestCase):
    def setUp(self):
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        scratch = pathlib.Path(self.scratch.name)
        self.probe = scratch / "probe"
        stand_in = scratch / "bin" / "golangci-lint"
        stand_in.parent.mkdir()
        stand_in.write_text(STAND_IN, encoding="utf-8")
        stand_in.chmod(stand_in.stat().st_mode | stat.S_IXUSR)
        self.path = f"{stand_in.parent}{os.pathsep}{os.environ['PATH']}"

    def lint(self, caller=None, cwd=REPO_ROOT):
        """Run the script with the caller's settings; return its exit status and what the linter saw."""
        environment = {**os.environ, "PATH": self.path, "LINT_PROBE": str(self.probe), **(caller or {})}
        done = subprocess.run(
            ["sh", str(LINT_SCRIPT)], cwd=cwd, env=environment, capture_output=True, text=True, check=False
        )
        seen = dict(line.split("=", 1) for line in self.probe.read_text(encoding="utf-8").splitlines())
        return done.returncode, seen

    def test_cache_is_inside_the_checkout(self):
        _, seen = self.lint()
        self.assertEqual(pathlib.Path(seen["cache"]), REPO_ROOT / ".cache" / "golangci-lint")

    def test_cache_stays_in_the_checkout_whatever_the_caller_set(self):
        _, seen = self.lint(caller={"GOLANGCI_LINT_CACHE": "/somewhere/shared"})
        self.assertEqual(pathlib.Path(seen["cache"]), REPO_ROOT / ".cache" / "golangci-lint")

    def test_toolchain_is_the_one_go_mod_names_whatever_the_caller_set(self):
        _, seen = self.lint(caller={"GOTOOLCHAIN": "local"})
        self.assertEqual(seen["toolchain"], module_toolchain())

    def test_lints_the_whole_module_from_the_checkout_root(self):
        _, seen = self.lint(cwd=REPO_ROOT / "scripts")
        arguments = seen["args"].split()
        self.assertEqual(pathlib.Path(seen["cwd"]), REPO_ROOT)
        self.assertEqual((arguments[0], arguments[-1]), ("run", "./..."))

    def test_does_not_wait_for_or_refuse_runs_in_other_checkouts(self):
        _, seen = self.lint()
        self.assertIn("--allow-parallel-runners", seen["args"].split())

    def test_linter_exit_status_is_returned(self):
        status, _ = self.lint(caller={"LINT_STATUS": "3"})
        self.assertEqual(status, 3)


if __name__ == "__main__":
    unittest.main()
