# Roadmap: deploymentproxy (formerly nsproxy)

Owned by Jody; changed only by an agent at Jody's request. Loopers propose in PROPOSALS.md.

## Now
nsproxy, one per cluster: a Kubernetes controller for `TenantNamespace` objects that creates and deletes a deployment's tenant namespaces (`tac-<deployment>-<tid>`) with baseline isolation, so no deployment needs cluster-wide rights.
Latest release v0.1.2 (2026-10-27), line 0.1.

## Next
- The rename to deploymentproxy: module, API group `deploymentproxy.tacplus.dev`, labels, image, docs; v0.2.0 (#9). Agent, at Jody's request, 2026-11-02 (`tacplus-dev/roadmap:decisions/deploymentproxy-2026-11-02.md`).
- Chaos faults as custom resources: pod faults in the controller, node faults in a separate privileged node agent reachable only through the controller; requesting deployment only (#10). Agent, at Jody's request, 2026-11-02.

## Later
- Nothing recorded yet.

## Not doing
- Deployment namespaces (`tac-<deployment>`): the top pipeline creates them; deploymentproxy handles tenant namespaces only (decisions/deployments-per-cluster-2026-10-26.md)
- Keeping a tenant's data on deletion: deletion is immediate; keeping data is the caller's responsibility (decisions/deployments-per-cluster-2026-10-26.md)
