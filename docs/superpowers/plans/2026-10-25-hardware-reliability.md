# MCP hardware reliability implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans task by task.

**Goal:** Make the personal fork reliably scan through MCP and restore usable hardware sessions.
**Architecture:** Shared existing Go transports, a hardened Linux launcher, and the maintained stock-firmware bridge FAP. No new broker or runtime dependency.
**Tech Stack:** Go, POSIX shell, Python process tests, C/Furi, ufbt.
**Spec:** ../specs/2026-10-25-hardware-reliability-design.md

## Global Constraints

- Retain stock OFW 1.3.4 support and Marauder v1.19.1 compatibility.
- Preserve risk gates, audit behavior, and untrusted-output handling.
- All waits and buffers are bounded; cancellation stops active scans.
- No machine-specific data or observed radio identifiers in commits.
- Preserve dirty dotfiles; serialize hardware and overlapping edits.

## Review Focus

- Bridge launch succeeds but the port reappears late: bounded failure with cause.
- Scan prompt appears immediately while RF work continues: honor requested duration and stop.
- AP names contain punctuation or resemble metadata: preserve SSID without field confusion.
- Existing file is longer than replacement or a retry occurs: never append duplicates.
- Host closes or unplug happens during pending CDC TX: terminate without deadlock/use-after-free.

### Task 1: MCP Marauder connection parity

**Files:** create cmd/promptzero/setup_marauder.go and setup_marauder_test.go; extract Marauder setup from setup.go; adjust MCP setup.
**Interfaces:** connectMarauder(context.Context, *config.Config, *flipper.Flipper) (*marauder.Marauder,error), with narrowly injected connection operations for tests.

- [x] Define route inputs and cleanup contract; map existing setup consumers.
- [x] RED tests for direct, BLE, single-cable/hybrid bridge, disabled, underlying errors, and suspended Flipper behavior.
- [x] Minimal shared connector; MCP returns wrapped initialization error when requested Wi-Fi is unavailable; preserve REPL/web nonfatal behavior.
- [x] Run focused package race tests and full short suite; commit with Codex attribution.

### Task 2: Official firmware JS and storage compatibility

**Files:** internal/flipper/commands.go, capabilities.go and new focused files/tests as appropriate; internal/tools/js.go.
**Interfaces:** preserve JSRun and StorageWrite public signatures.

- [x] RED transport tests prove OFW 1.3.4 JS execution and replacement of a longer existing file, zero bytes, remove/write failures, and retry without duplicate append.
- [x] Add capability-based JS support and safe replacement semantics without changing append-dependent generic primitives unnecessarily.
- [x] Run internal/flipper and internal/tools race tests; commit the focused fixes.

### Task 3: Compatible structured AP scans

**Files:** internal/marauder/commands.go, parse.go; new scan_ap.go, scan_ap_test.go, AP detail parser/tests; internal/tools/wifi.go only for necessary delegation/description changes.
**Interfaces:** retain ScanAPParsedCtx/ScanAPParsedStream and ScanResult; extend AccessPoint with security/hidden fields without breaking consumers.

- [x] RED serial-script tests using synthetic v1.19.1 replies: immediate prompt, bracketed AP list, detail fields, hidden SSID, cancellation, stop failure, malformed detail, and bounded indices/duration.
- [x] Use scanall for current firmware, collect for the requested interval, always stop, then list and enrich APs with info -a. Preserve raw evidence, reject unexpected responses, and exclude station rows.
- [x] Confirm any legacy command handling from firmware evidence rather than version guesses.
- [x] Run marauder/tools race tests and commit.

### Task 4: Reliable launcher and discovery diagnostics

**Files:** contrib/linux/promptzero-mcp-wrapper; contrib/linux/test_wrapper.py; docs/hardware-mcp.md; optional small smoke client under scripts/.
**Interfaces:** FLIPPER_PORT and PROMPTZERO_BIN override installation; stdout reserved for MCP; nonzero startup errors on stderr; lifetime lock keyed to stable device identity.

- [x] RED subprocess/PTY tests for absent/non-character device, missing dependency/binary, failed stty, competing launchers, an unrelated device owner, symlink aliases, child exit/signal propagation, and clean MCP stdout.
- [x] Implement minimal shell launcher with bounded readiness and clear busy diagnostics; retain raw/no-echo and pass the resolved port consistently to PromptZero.
- [x] Add an MCP smoke client with bounded requests and no model key that checks initialization/list/call and records structured results locally.
- [x] shellcheck, Python lint and process tests; commit.
- [ ] Verify current-thread Codex discovery through its supported diagnostics before changing global waits.

### Task 5: Custom FAP transport and lifecycle

**Files:** contrib/flipper-uart-bridge/ (existing source/manifest/icon imported with provenance), pure policy helpers and host tests; internal/marauder/bridge.go or companion file for custom bridge DTR cleanup if needed.
**Interfaces:** 115200 8N1 transparent bytes, CDC_DATA_SZ packet cap, completion-driven TX, bounded loss counters, Back/disconnect/armed DTR closure.

- [x] Inspect matching OFW USB implementation and validate callback semantics before relying on them.
- [x] RED host C tests for packet conservation/completion, overflow accounting, DTR startup glitches/arming/close, disconnect, and tick wrap; transport tests for opt-in host DTR cleanup if needed.
- [x] Move blocking USB-to-UART work to workers, make callbacks bounded, make teardown single-owner/idempotent, restore CLI/Expansion after exit.
- [x] Compile host tests with warnings as errors and sanitizers; ufbt build against stock SDK; commit.

### Task 6: Integrate, validate, document, and deliver

**Files:** runbook and smoke tests; installed binary/wrapper/FAP with backups; narrowly reconcile related chezmoi sources.
**Interfaces:** unchanged existing MCP registration except demonstrated necessary corrections; no permanent risk-gate overrides.

- [x] Back up/check installed artifacts, build fork, deploy reversible local artifacts and FAP using supported storage RPC.
- [ ] MCP initialize/list, wifi_scan_ap structured fields, shutdown/CLI recovery, second scan session, and competing launcher test; keep radio output private.
- [x] Verify Flipper-only path and document single-cable Wi-Fi/core switching limitations.
- [x] Run task ci and added host tests; review each task and the final branch; fix findings with regression tests.
- [ ] Update verified runbook and fallback script; publish reviewable branches in the personal fork with exact validation evidence. No PR creation or merge without a separate request.

Final hardware acceptance remains pending after USB disconnection: upload the corrected JS fallback, repeat native scans and CLI recovery, and verify the active Codex client catalog. The custom FAP, wrapper, and Go binary are installed; original artifacts are backed up.
