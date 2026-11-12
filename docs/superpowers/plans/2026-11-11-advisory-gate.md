# Documented advisory gate implementation plan

> For agentic workers: use superpowers:executing-plans and test-driven-development for this focused repair.

Goal: make local and CI vulnerability gates enforce the documented mitigation without accepting other vulnerable call paths or broken scans.
Architecture: a Python standard-library entrypoint runs pinned govulncheck JSON, validates the report, classifies called findings, and proves the known redirect mitigation before granting its narrow exception.
Spec: ../specs/2026-11-11-advisory-gate-design.md

## One coherent change

Files: new scripts/check_vulnerabilities.py and scripts/test_check_vulnerabilities.py; update Taskfile.yml, .github/workflows/ci.yaml, docs/security/known-advisories.md and the existing hardware test runner if needed so policy tests run in CI.

- [x] State structures/interfaces and the touched entrypoints before implementation.
- [x] Use the captured real report at /tmp/promptzero-main-vuln.json as a schema reference; do not infer a successful scan from a JSON-mode zero exit.
- [x] Write command-boundary regression tests first: clean report; known called finding with passing parent/http/sse tests; any other called ID; changed module/version; fixed version available; malformed/truncated/missing config or trace; scanner nonzero; missing, skipped or failing mitigation tests.
- [x] Observe RED before implementing. The existing hosted CI failure independently reproduces the policy mismatch.
- [x] Implement the smallest gate; preserve raw findings and error context. Do not change production code or dependencies.
- [x] Wire both task vuln and CI to it; run the Python tests, real scan, exact Go regression and hardware script. Inspect the diff for fail-open cases.
- [ ] Parent performs review, commit, draft PR and integration. Worker leaves changes uncommitted.

Review focus: JSON mode succeeds despite findings; a go test selector matching nothing also succeeds; malformed findings must never be silently ignored; a changed dependency or newly available fix requires re-review; operational errors are failures.
