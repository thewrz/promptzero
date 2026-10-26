# Flipper and Marauder MCP workflow

This fork supports stock Flipper firmware with the custom USB–UART bridge in
`contrib/flipper-uart-bridge`. A single USB cable has one owner and one mode:
Flipper CLI/RPC, or the GPIO Marauder bridge. Exit the current MCP server before
switching modes. The custom app restores the Flipper CLI when its armed DTR
line falls. Other bridge apps must leave `bridge_exit_dtr` disabled unless they
implement the same behavior.

## Build and install

Use the Go toolchain declared in `go.mod`. The development tools are installed
with `task dev:setup`; include `$(go env GOPATH)/bin` in `PATH`.

```sh
export GOTOOLCHAIN=go1.26.10
task ci
task hardware:test
install -m 755 bin/promptzero "$HOME/.local/bin/promptzero-fork"
install -m 755 contrib/linux/promptzero-mcp-wrapper \
  "$HOME/.local/bin/promptzero-mcp-wrapper"
```

The wrapper prefers executable `~/.local/bin/promptzero-fork` and falls back to
`~/.local/bin/promptzero`. Explicit `PROMPTZERO_BIN` overrides both. This keeps a
separately managed upstream executable available without rolling MCP back when
dotfiles restore that upstream binary.
Set `FLIPPER_PORT` to the device's `/dev/serial/by-id/…` path. The launcher
preserves that identifier, checks permissions, waits up to five seconds for the
device, sets raw/no-echo mode, and holds locks until the server exits. Errors go
to stderr; stdout remains MCP JSON. A busy error names the owning process when
available. Close that session or qFlipper before retrying.

Build the app with a compatible stock-firmware SDK:

```sh
cd contrib/flipper-uart-bridge
ufbt lint
ufbt
```

Back up the existing SD-card app before copying `dist/usb_uart_bridge.fap` to
`/ext/apps/GPIO/usb_uart_bridge.fap` with qFlipper or the SDK storage utility.
Close qFlipper before launching MCP. Updating the app does not require replacing
Flipper firmware. Generated app binaries are not committed.

## MCP registration

Configure the installed wrapper as the command. Use these arguments for Wi-Fi:

```text
--mcp --wifi --marauder-bridge --marauder-bridge-exit-dtr
--marauder-bridge-command "loader open /ext/apps/GPIO/usb_uart_bridge.fap"
```

For Flipper-only tools, use `--mcp --no-wifi` instead. `--no-wifi` overrides
`marauder.enabled` in the configuration. It conflicts with explicit Wi-Fi,
bridge, and Marauder BLE connection flags. Do not register both modes as
simultaneously starting servers for one serial device.

The YAML equivalents for the custom bridge are:

```yaml
marauder:
  enabled: true
  bridge: true
  bridge_command: "loader open /ext/apps/GPIO/usb_uart_bridge.fap"
  bridge_exit_dtr: true
  baud_rate: 115200
```

The DTR option is opt-in. PromptZero asserts DTR and allows at least 500 ms for
the app to arm before lowering it during cleanup, including startup
cancellation. The app first requires a low-DTR snapshot or disconnect, so the
inherited CLI connection cannot arm shutdown during USB handoff. Back also exits
the app. If an older app is still installed, use Back to return to the CLI.

## Run a fresh scan

From the repository, set the same environment as the MCP client and run:

```sh
export PROMPTZERO_BIN="$HOME/.local/bin/promptzero-fork"
export FLIPPER_PORT=/dev/serial/by-id/REPLACE_WITH_YOUR_FLIPPER

python3 scripts/mcp-smoke.py --port "$FLIPPER_PORT" \
  --tool wifi_scan_ap --duration 5 --timeout 90 \
  --bridge-command 'loader open /ext/apps/GPIO/usb_uart_bridge.fap' \
  --bridge-exit-dtr --report /tmp/promptzero-scan.json
```

This performs MCP initialization, lists tools, checks the selected tool's risk
metadata, and calls `wifi_scan_ap`. It requires no model key or high/critical
risk override. Evidence files are created with mode 0600 and are never
overwritten. They include raw device output and nearby network identifiers;
keep them local.

The scanner runs `scanall` for the requested interval even when firmware
immediately returns a prompt, sends `stopscan`, then reads `list -a` and
`info -a` details. Each AP includes `ssid`, `bssid`, `channel`, `rssi`, `security`
(the firmware's auth label), and `hidden`. Raw evidence is retained. At most
64 APs are enriched; `truncated` and `limit` identify a capped result.
Station rows are not returned as APs. A command or parsing failure is an error,
not an empty successful scan. `list -a` alone only reads stored results.

The stock-JS fallback `contrib/flipper-uart-bridge/marauder_scan.js` also starts
and stops a fresh five-second scan before reading results. It prints raw detail
for indexes 0–10 and states that limit. Use the MCP operation above for structured
results and the larger 64-AP bound. Run the JS fallback in Flipper-only mode;
the bridge app and JS serial module cannot own the GPIO UART simultaneously.

After the scan process exits, verify recovery with a second, Flipper-only
session:

```sh
python3 scripts/mcp-smoke.py --port "$FLIPPER_PORT" \
  --tool device_info --timeout 30 --report /tmp/promptzero-device.json
```

MCP Wi-Fi startup now fails with the actual bridge or transport error if the
requested Marauder connection cannot be established. USB launch is sent once;
a lost CLI prompt during handoff must be followed by a successful Marauder
firmware/version probe before startup is accepted. A missing Wi-Fi client is
not silently accepted. Single-cable Flipper-only operations report the suspended
transport while the bridge owns USB; a separate BLE Flipper connection is needed
for simultaneous core and Wi-Fi access.

## Discovery and checks

First verify the wrapper and smoke operation. A running process alone does not
prove MCP initialization or tool discovery succeeded. Check the MCP client's
server status and stderr for a handshake error or a busy port before changing
timeouts. Codex's optional-server grace and per-server startup timeout are
separate settings; a larger per-server timeout does not change the grace.
Do not make an unplugged hardware server mandatory for every Codex session.

`task hardware:test` runs the Linux PTY/process tests, MCP protocol tests, and
sanitized C bridge-policy tests without hardware. `task ci` runs Go lint, vet,
build, full race tests, and vulnerability checks. App compilation uses `ufbt`;
physical USB handoff and repeated scan sessions require the actual devices.

The Bluetooth module is pinned to the reviewed Linux scan-cancellation fix in
[`thewrz/bluetooth`](https://github.com/thewrz/bluetooth/commit/625592a24264d96ad610bc520568ca0f36055ac8).
It retains TinyGo Bluetooth's BSD license. The pin removes the need for a local
workspace override when running race tests on a Linux host with a Bluetooth
adapter.
