# USB-UART Bridge FAP

This maintained source imports the existing USB-UART Bridge app. Its original
manifest identifies the app author as `thewrz`, version `1.0`, and describes it
as a GPIO UART passthrough. The original app source, manifest, and icon were
preserved as the starting point; this fork adds bounded transport queues,
completion-driven USB packets, loss reporting, and lifecycle handling.

The bridge ignores an initial DTR-high snapshot until it has observed DTR low
or a USB disconnect. This allows a CLI-to-bridge USB handoff without treating
the CLI's preexisting high DTR as a close request. Once eligible, the host
requests shutdown by keeping DTR high for 500 ms, then lowering it. A disconnect
exits after the bridge has armed; transient startup disconnects are ignored.
UART traffic remains byte-transparent.

The source copy did not include repository metadata or a license file, so this
directory does not claim additional license terms. No built `.fap` or `.elf`
artifacts are tracked here.

Build from this directory with `ufbt` using the installed Flipper SDK.

Run the host policy tests from the repository root with:

```sh
cc -x c -std=c11 -Wall -Wextra -Werror -pedantic \
  -fsanitize=address,undefined -g \
  contrib/flipper-uart-bridge/tests/bridge_policy_test.c.in \
  contrib/flipper-uart-bridge/bridge_policy.c \
  -o /tmp/bridge_policy_test && /tmp/bridge_policy_test
```
