#!/bin/sh
# Run hardware-independent launcher, MCP protocol, and bridge policy checks.
set -eu

cd "$(dirname "$0")/.."
for dependency in python3 node cc shellcheck; do
    command -v "$dependency" >/dev/null 2>&1 || {
        printf 'Missing hardware test dependency: %s\n' "$dependency" >&2
        exit 1
    }
done

shellcheck contrib/linux/promptzero-mcp-wrapper
python3 -m unittest discover -s contrib/linux -p 'test_*.py'
python3 -m unittest discover -s scripts -p 'test_check_vulnerabilities.py'
python3 -m unittest discover -s scripts -p 'test_mcp_smoke.py'
node --test contrib/flipper-uart-bridge/tests/marauder_scan.test.cjs

test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
cc -x c -std=c11 -Wall -Wextra -Werror -pedantic \
    -fsanitize=address,undefined -g \
    contrib/flipper-uart-bridge/tests/bridge_policy_test.c.in \
    contrib/flipper-uart-bridge/bridge_policy.c \
    -o "$test_dir/bridge-policy-test"
"$test_dir/bridge-policy-test"
