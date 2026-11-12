#!/bin/sh
# Run golangci-lint over the module, independent of the caller's Go and of other checkouts.
#
# Toolchain: golangci-lint refuses to analyse with a Go newer than the one it was built with
# ("file requires newer Go version"), which happens as soon as the system Go moves ahead of the
# module's. The run is pinned to the toolchain go.mod names.
#
# Cache: golangci-lint records each finding with the absolute path of the checkout that produced
# it, and by default every checkout shares one cache. A second worktree then gets the first one's
# paths replayed; once that worktree is removed the `//nolint` lines cannot be read and suppressed
# findings are reported. The cache is kept inside this checkout, so nothing is replayed from
# elsewhere.
#
# Lock: golangci-lint also takes a machine-wide lock when it starts and exits with "parallel
# golangci-lint is running" while another run holds it. With a cache per checkout, runs in
# different checkouts have nothing to protect from each other, so they proceed together.
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

toolchain=$(sed -n 's/^toolchain //p' go.mod)
if [ -z "$toolchain" ]; then
  printf 'lint.sh: go.mod names no toolchain to pin the lint run to\n' >&2
  exit 2
fi

GOTOOLCHAIN=$toolchain
GOLANGCI_LINT_CACHE=$root/.cache/golangci-lint
export GOTOOLCHAIN GOLANGCI_LINT_CACHE

exec golangci-lint run --allow-parallel-runners ./...
