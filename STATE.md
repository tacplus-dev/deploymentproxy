# State: deploymentproxy

Overwritten to stay current (loopers and agents). Last updated: 2026-11-02 by looper (issue #9).

- **Version / line:** v0.2.0 (https://github.com/tacplus-dev/deploymentproxy/releases/tag/v0.2.0), line 0.2: the rename (formerly nsproxy, #9), breaking: module `github.com/tacplus-dev/deploymentproxy`, API group `deploymentproxy.tacplus.dev`, labels `deploymentproxy.tacplus.dev/*`, image, namespace, service account, ClusterRole, Deployment and ConfigMap `deploymentproxy`; Go floor go 1.27.3 (go.mod); no tacplus.yaml
- **Works:** the `TenantNamespace` controller: tenant namespaces `tac-<deployment>-<tid>` with RoleBinding, NetworkPolicies, ResourceQuota and shared-tenant (`restore`) traffic; gates `make check` (envtest, and a test that no file but HISTORY.md and ROADMAP.md names the former name) and `make image`; govulncheck reports no vulnerabilities (source and binary)
- **Released / deployed:** image ghcr.io/tacplus-dev/deploymentproxy:0.2.0 and line tag 0.2 (linux/amd64, linux/arm64); the 0.1 line was published as ghcr.io/tacplus-dev/nsproxy (formerly nsproxy) and is not deployed anywhere
- **Known gaps:** downstream references still use the former name (tacplus-dev/operator#3 and #18, `deployments-per-cluster-2026-10-26.md` item 4, the fleet manifests); ROADMAP.md's "Now" still uses it (Jody's file)
- **Blocked on:** nothing
