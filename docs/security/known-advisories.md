# Known advisories

Advisories that the pinned govulncheck source scan reports and that cannot yet be cleared by a
dependency upgrade. The shared `scripts/check_vulnerabilities.py` gate consumes its JSON stream,
preserves the raw findings in command output, and applies the narrow policy below. JSON mode exits
zero even when it reports vulnerabilities, so the gate evaluates the findings itself. Module-only
and package-only findings remain informational, matching govulncheck's symbol-level default.
Re-check this note whenever the affected dependency changes.

## GO-2026-6911 — `github.com/mark3labs/mcp-go` follows redirects with headers attached

- **Status in the library (2026-11-11):** no fixed release is available.
- **The flaw:** the streamable-HTTP client follows a server's `3xx` `Location` to any host and
  attaches configured headers and OAuth credentials to the redirected request.
- **Where this repository calls it:** `internal/mcpfed/managed.go` builds the configured external
  MCP federation clients for `http` and `sse` transports. Both use an HTTP client that refuses
  redirects, so configured headers cannot be replayed to another host. A server behind a redirect
  must be configured with its final URL. `TestFederationClientsDoNotFollowRedirects` exercises both
  transports.
- **Why `govulncheck` still reports it:** the scanner matches on called symbols, not on the HTTP
  client's redirect configuration.
- **CI exception:** only called `GO-2026-6911` findings for `github.com/mark3labs/mcp-go@v1.3.0`
  with no `fixed_version` are eligible. For the `NewStreamableHTTP` constructor finding (which has
  no receiver in govulncheck's trace), the first frame in this repository must be
  `github.com/xunholy/promptzero/internal/mcpfed.buildClient`. The gate then runs
  `TestFederationClientsDoNotFollowRedirects` with `-json -race -count=1` and requires explicit
  `pass` events for the parent test and its `/http` and `/sse` subtests. The gate fails on called
  findings for other advisories, changed modules or versions, a published fix, malformed scan,
  scanner error, and missing/skipped/failed tests. The test confirms that neither HTTP nor SSE
  transport requests its redirect target.
- **Current stdio traces:** govulncheck also reports `SendRequest` and `SendNotification` through
  `cmd/mifaretest.main`. That command currently constructs an mcp-go stdio client, so these are
  conservative dispatch traces through stdio rather than streamable-HTTP clients. They are benign
  with the current construction path; reassess if `cmd/mifaretest` or its client setup changes.
- **Trace limitation:** govulncheck v1.9.0 emits one call stack per vulnerable symbol. The gate
  rejects a different observed caller for the constructor symbol, but another constructor call
  site may not replace the one stack the scanner emits, so unchanged trace output cannot prove that
  every call site remains the same.
- **Remove this entry and the gate exception when:** `mcp-go` ships a fixed version and `go.mod`
  requires it; also update the finding tests and allowlist.
