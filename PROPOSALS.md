# Proposals: deploymentproxy

Written by loopers. Each proposal: what, why, links. Jody promotes accepted ones into ROADMAP.md (via an agent);
the looper then marks it `promoted` or `declined` here with the date.

## Open
- **govulncheck in the gates** (2026-10-28, #7): #7 was found by the nightly scan, not by CI; running
  `govulncheck ./...` in `make check` would fail a push that reaches a known vulnerability. It would also fail
  CI when a new advisory lands without any change, which is the trade-off to decide. #8 adds a case: an advisory
  deploymentproxy does not reach in source still ships in the binary and fails a `govulncheck -mode binary` scan
  of the image, so the gate would want both modes (source on `make check`, binary on `make image`). #17
  (2026-11-11) is a third: GO-2026-6859 in go1.27.3's net/http, again found by the nightly scan. The installed
  govulncheck there was built with go1.26 and could not load the go1.27 packages, so the gate should build its
  govulncheck with the floor toolchain. The version matters too (nightly scan, 2026-11-12): govulncheck v1.1.4
  built with go1.27.3 loads the packages but panics in its x/tools v0.29.0 (`unexpected expr: *ast.KeyValueExpr`);
  v1.9.0 built with go1.27.3 runs and reports GO-2026-6859 (#17). The build pipeline's own govulncheck has the
  same problem (tacplus-dev/looper#109).
- **PodFault: a cap on live faults per deployment** (2026-11-02, #10): a deployment's chaos controller can create
  any number of PodFaults at once, and each may fail or kill one pod; only one `Failure` per pod is enforced. The
  chaos survey wants one fault at a time per fleet (tacplus-dev/chaos#1, `Forbid`), which the chaos controller does
  itself. A cap in deploymentproxy (say, at most N live faults per deployment, from the ConfigMap) would hold that
  bound even if the chaos controller misbehaves, at the cost of one more refusal reason.
- **PodFault: a quorum guard** (2026-11-02, #10): deploymentproxy kills whichever eligible pod the selector
  matches, even the last Ready replica of a service. The survey's controller checks "never a service with fewer
  than 2 Ready pods"; deploymentproxy could refuse it too (`WouldBreakQuorum`), as the side that acts.
- **PodFault in a real cluster** (2026-11-02, #10): envtest has no kubelet, so the tests prove the API side only
  (deleted, patched, put back). A kind/k3d job, which #10 already asks for the node agent, should also run a
  `Failure` and check the pod goes not-Ready and comes back Ready after the revert.
- **PodFault: a Kill retried after its pod went away** (2026-11-02, #10): a Kill whose status write fails after
  the delete is retried on the recorded pod UID, and a pod that is gone by then is taken as killed (`Applied`).
  If the delete itself had failed and the pod went away for another reason (a rollout) before the retry, the
  status reports a kill deploymentproxy did not do. Recording "delete accepted" needs one more status write
  between the delete and `Applied`; worth it only if triage is misled in practice.
- **Drop `sharedTids` from the config** (2026-11-08, #14): it only shaped the NetworkPolicies #14 removed, and is
  still accepted (with no effect) so that existing ConfigMaps load under `UnmarshalStrict`. The next minor could
  remove the field, after the deployments' ConfigMaps no longer set it.
- **The security run's MEDIUM findings** (2026-11-08, #13): the build pipeline's `looper-scan` of main reports,
  below the policy's HIGH/CRITICAL bar, nine actions used by a mutable tag in `.github/workflows/ci.yml` and
  `release.yml` (semgrep `github-actions-mutable-action-tag`: `actions/checkout@v4`, `setup-go@v5`, the
  `docker/*` actions), and gosec G304 on `config.Load`'s `os.ReadFile(path)` (the path is the operator's
  `--config` flag, so not a traversal). Pinning the actions by commit SHA closes the first; the second wants a
  `security-exceptions.yaml` entry if it is ever raised to the policy. The nightly scan of 2026-11-17 (trivy now
  reads the manifests) adds, below the bar: KSV-0048 (MEDIUM, the ClusterRole's pod `patch`/`delete`, which
  PodFault needs), KSV-0125 (MEDIUM, `config/manager/deployment.yaml`'s image not from a trusted-registry list),
  KSV-0011 (LOW, no CPU limit), KSV-0020/KSV-0021 (LOW, UID/GID 65532 is not above 10000; this is distroless's
  `nonroot`), DS-0026 (LOW, no `HEALTHCHECK`, which Kubernetes ignores in favour of its probes). A CPU limit is
  the one change with an effect. The others want exceptions with reasons if they are ever raised to the policy.
- **`go mod tidy` in the gates** (2026-11-14, #20): `go mod tidy -diff` on main wants `k8s.io/utils` moved from
  the indirect requires to the direct ones (the code imports it), and nothing checks it. A `tidy -diff` step in
  `make check` would keep go.mod honest; the fix itself is one `go mod tidy`.
- **The image built before a release** (2026-11-17, #23): nothing builds the `Dockerfile` until a tag's release
  run. The gates (`make check`) run without Docker, `ci.yml` does not build the image, and loopers cannot
  dispatch release.yml's dry run (`workflow_dispatch` answers 403 to their token). So #23's change of build stage
  (`golang:1.27-alpine`) was first built for linux/amd64 and linux/arm64 by a member's dry run or by the next
  tag. A `docker buildx build --platform linux/amd64,linux/arm64` job in `ci.yml` (no push), on pushes that touch
  the `Dockerfile`, `go.mod` or `go.sum`, would catch a broken image at the pull request instead of at release.

## Promoted / declined
