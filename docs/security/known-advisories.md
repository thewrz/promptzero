# Known advisories

Advisories that `govulncheck ./...` reports and that cannot yet be cleared by a dependency upgrade.
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
- **Remove this entry when:** `mcp-go` ships a fixed version and `go.mod` requires it.
