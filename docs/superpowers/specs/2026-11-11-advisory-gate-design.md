# Documented vulnerability mitigation gate

Promotion to main exposed a known mismatch: govulncheck cannot inspect the configured redirect refusal, so GO-2026-6911 remains a symbol finding despite the existing HTTP/SSE regression tests. The user authorized fixing merge blockers and synchronizing both repositories. This is a CI repair, with no new product behavior or dependencies.

Use govulncheck v1.9.0 JSON output and enforce its findings explicitly. JSON mode returns success even for vulnerable code; process exit status alone is insufficient. Preserve the raw report. Allow only GO-2026-6911 in github.com/mark3labs/mcp-go v1.3.0, only while the finding has no fixed_version, and only after the parent redirect regression plus its http and sse subtests actually pass. New called findings, version changes, malformed or incomplete output, scanner errors, and missing/skipped/failed mitigation tests fail closed. Non-called module/package findings retain the scanner's default informational treatment.

A small Python standard-library script fits the existing host checks. CI and task vuln must use the same gate. Tests exercise its command boundary using process-edge fixtures, plus a real scan and the real Go mitigation tests. Do not disable a CI job or turn scanner failures into warnings. A dependency upgrade cannot clear this finding yet; unconditional failure cannot represent the already verified configuration mitigation.

References: https://go.googlesource.com/vuln/+/refs/tags/v1.9.0/cmd/govulncheck/doc.go and docs/security/known-advisories.md.
