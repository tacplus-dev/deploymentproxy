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
- **TestPutBack flake** (2026-11-02, #9): once in five runs under load, `TestPutBack` failed at
  `controller_test.go:391: tac-eps-abcdefgh is being deleted` (the namespace deleted by hand, with its finalizers
  cleared by `removeNamespace`, still had a deletion timestamp 30s later); the four reruns passed. Worth finding
  what holds the namespace (a finalizer the test does not clear, or deploymentproxy's update racing the
  finalize) before it fails a land.
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
- **Stop removing the retired NetworkPolicies** (2026-11-08, #14): every reconcile issues two DELETEs (answered
  NotFound once they are gone) for `deploymentproxy-default-deny` and `deploymentproxy-allow`. Once every cluster
  has run a version with #14, a later minor can drop `retiredObjects` and the `delete` grant on networkpolicies.

## Promoted / declined
