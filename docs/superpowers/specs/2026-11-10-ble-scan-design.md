# Bounded BLE discovery and scoped Marauder CLI

Authorized by the user for both thewrz/promptzero and thewrz/wrz-flipper-rta.
Preserve each fork's existing features and deployment configuration.

Extend wifi_sniff_bt with explicit target_type=all, emitting bare sniffbt.
Retain airtag/flipper/flock/meta filters. Collect advertisements for 1–60 seconds
(default 30), stopping any previous scan before starting and confirming stopscan on expiry or
cancellation. Continue collecting after the firmware startup prompt; it does not
signal scan completion. Bound captured output and surface cleanup errors. This is advertisement
discovery, not capture of an established Bluetooth audio connection.

Add wifi_read_cli for exactly help, info, sniffbt, sniffbt -t with the four
existing filters, and stopscan. Reject all other strings, including control
characters and command chaining, before serial I/O. Keep existing risk gates;
the new tool uses the same Medium classification as Bluetooth discovery.

Approaches: extending the typed scan plus a strict CLI allowlist meets the request.
Unrestricted passthrough would expand permissions unnecessarily. A separate scan
tool would duplicate the existing interface. No firmware flashing or new dependencies.

The implementation lives in a task worktree for each repo. No commit, push, merge,
or fleet deployment is included without explicit authorization. Live validation
uses the RTA build to retain the installed spectrum functionality.

Live validation on the ESP32-C5 / Marauder v1.10.2 confirmed the startup prompt
arrives before asynchronous scan output. The final MCP call ran for 15.3 seconds,
reported BLE scan startup, and left info responding normally. No advertisements
were returned during that trial. No phone/earbud connection was identified.
The firmware uses active scanning, which may send BLE scan requests.

References:
- https://github.com/justcallmekoko/ESP32Marauder/wiki/sniffbt
- https://github.com/justcallmekoko/ESP32Marauder/blob/v1.10.2/esp32_marauder/CommandLine.cpp
- https://github.com/justcallmekoko/ESP32Marauder/blob/v1.10.2/esp32_marauder/WiFiScan.cpp
