# MCP hardware reliability

## Intent and accepted scope

The operator requested a personal upstream-compatible fork and implementation of
the diagnosed MCP bridge, firmware compatibility, launcher, discovery, and scan
workflow improvements, including the existing custom Flipper USB-UART app.
Success is a repeatable structured Wi-Fi scan through MCP on official firmware,
with bounded startup and cleanup and useful failure messages.

## Approach

Use the existing Go transports and MCP registry, plus a small Linux launcher and
the existing C bridge app. Fix the device boundaries instead of retaining the
one-off Python client/JavaScript workaround. A wrapper-only workaround would
duplicate protocol logic; a new device broker would be a larger architecture
change than these defects require. The user's fork request selects the first
approach. Keep the Go module path and upstream license intact.

## Constraints

- Retain stock OFW 1.3.4 support and Marauder v1.19.1 compatibility.
- Preserve risk gates, audit behavior, and untrusted-output handling.
- No blanket enabling of high/critical tools, firmware replacement, RF attacks,
  shared-main pushes, upstream PRs, or merges.
- All waits and buffers are bounded; cancellation stops active scans.
- Preserve existing dirty dotfiles work. Import the launcher and custom FAP into
  this fork as maintained sources; integrate local copies with backups and
  content checks only after validation.
- Do not publish SSIDs, BSSIDs, machine identifiers, secrets, or local paths.
- Keep changes in focused commits and draft PRs; hardware access is serialized.
- TDD pins behavior at transport, tool, process, and FAP policy boundaries.

## Components

1. A shared Marauder connector selects direct serial, direct BLE, or bridge.
   Requested Wi-Fi connection failures in MCP fail startup with their cause.
   REPL/web retain their documented degraded operation. Single-cable bridge
   mode explicitly suspends core Flipper access; hybrid BLE retains it.
2. Firmware compatibility uses supported commands and actual capabilities.
   Stock JavaScript works; storage overwrite has true replacement semantics.
3. One Wi-Fi tool performs scan, stop, list, and bounded per-AP detail reads,
   returning structured SSID/BSSID/channel/RSSI/security plus raw evidence.
   Station rows never become APs; errors are not empty successful scans.
4. The launcher uses a stable serial path, checks prerequisites, protects its
   full lifetime with a per-device lock, reports busy owners and setup errors
   on stderr, and preserves MCP stdout. A process lock coordinates launchers;
   OS serial exclusion and busy checks cover other clients where supported.
5. The custom FAP moves blocking UART work off callbacks, sends bounded CDC
   packets with real completion ordering, reports overflow, and unifies Back,
   disconnect, and armed DTR-close cleanup. UART payload bytes stay transparent.
6. A reproducible MCP smoke client and runbook cover discovery, structured scans,
   reconnect, failure messages, and rollback. Codex discovery changes require
   evidence rather than assuming a timeout is the cause.

## Validation and delivery

Use upstream Go tests/lint/vet/build/vulnerability checks, launcher process tests,
host C policy tests, and ufbt build. Exercise the installed fork through MCP on
the connected hardware, including a second session after shutdown and a competing
launcher. Keep scan transcripts local. Deploy only backed-up, tested artifacts.
The custom app's host-close behavior must be proven before relying on automatic
CLI restoration; if hardware exposes a limitation, report it precisely.
