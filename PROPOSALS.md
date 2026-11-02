# Proposals: deploymentproxy

Written by loopers. Each proposal: what, why, links. Jody promotes accepted ones into ROADMAP.md (via an agent);
the looper then marks it `promoted` or `declined` here with the date.

## Open
- **govulncheck in the gates** (2026-10-28, #7): #7 was found by the nightly scan, not by CI; running
  `govulncheck ./...` in `make check` would fail a push that reaches a known vulnerability. It would also fail
  CI when a new advisory lands without any change, which is the trade-off to decide. #8 adds a case: an advisory
  deploymentproxy does not reach in source still ships in the binary and fails a `govulncheck -mode binary` scan
  of the image, so the gate would want both modes (source on `make check`, binary on `make image`).
- **TestPutBack flake** (2026-11-02, #9): once in five runs under load, `TestPutBack` failed at
  `controller_test.go:391: tac-eps-abcdefgh is being deleted` (the namespace deleted by hand, with its finalizers
  cleared by `removeNamespace`, still had a deletion timestamp 30s later); the four reruns passed. Worth finding
  what holds the namespace (a finalizer the test does not clear, or deploymentproxy's update racing the
  finalize) before it fails a land.

## Promoted / declined
