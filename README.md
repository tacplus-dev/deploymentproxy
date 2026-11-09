# deploymentproxy

The one component per cluster that holds cluster privilege on a deployment's behalf. Today it creates and
deletes **tenant namespaces** for the deployments on a Kubernetes cluster, so that a deployment needs no
privilege outside its own namespace. It was formerly nsproxy, renamed in v0.2.0
([decision](https://github.com/tacplus-dev/roadmap/blob/main/decisions/deploymentproxy-2026-11-02.md)).

Several deployments share one cluster. The top pipeline creates each deployment's namespace,
`tac-<deployment>`, and deploys deploymentproxy once per cluster. A deployment's operator, running in
`tac-<deployment>`, asks deploymentproxy to create or delete its tenants' namespaces, `tac-<deployment>-<tid>`
(a tid is 8 characters of `[0-9a-z]`; all-digit ids are reserved, `people` is `00000001`, `restore` is
`00000002`). deploymentproxy is the only thing holding the cluster-wide permission, and it uses it narrowly.

Work arrives as issues; see github.com/tacplus-dev/looper.

## How it works

deploymentproxy is a Kubernetes controller for one custom resource, `TenantNamespace`
(`deploymentproxy.tacplus.dev/v1`). It has no network listener. The operator creates one in its own namespace:

```yaml
apiVersion: deploymentproxy.tacplus.dev/v1
kind: TenantNamespace
metadata:
  name: acme
  namespace: tac-prod
spec:
  tid: a1b2c3d4   # 8 of [0-9a-z], immutable
```

and waits for `status.conditions` `Ready=True`; `status.namespace` is then `tac-prod-a1b2c3d4`. When it is
refused, `Ready=False` and the reason says why (`NotDeploymentNamespace`, `InTenantNamespace`,
`InvalidDeployment`, `ReservedTid`, `NamespaceConflict`). Deleting the object deletes the namespace, at once.

- **Who may ask** is Kubernetes RBAC: the Role in [config/deployment-namespace](config/deployment-namespace),
  applied by the pipeline in each deployment namespace, lets that namespace's `operator` service account
  create `TenantNamespace` objects there and nowhere else.
- **The deployment** comes from the parent namespace's `tacplus.dev/deployment` label (set by the pipeline),
  never from its name. Objects in an unlabelled namespace, or in a tenant namespace, are refused.
- **The tenant namespace** carries `deploymentproxy.tacplus.dev/deployment` and
  `deploymentproxy.tacplus.dev/tid` labels and holds: a RoleBinding making `operator` of the deployment
  namespace `admin` there, and a ResourceQuota from the ConfigMap, always with no load balancers and no node
  ports. Anything changed or deleted by hand, the namespace included, is put back.
- **No NetworkPolicies:** isolation is the bearer token checked at every door (tacplus's generated
  authorizers), not reachability: a cross-deployment or cross-tenant call is refused by the callee, whatever
  the network lets through
  ([decision](https://github.com/tacplus-dev/roadmap/blob/main/decisions/s2s-never-crosses-tenants-2026-10-31.md),
  addendum 2026-11-07 (2)). deploymentproxy keeps none in a tenant namespace, and removes on reconcile the
  baseline ones earlier versions kept (`deploymentproxy-default-deny`, `deploymentproxy-allow`); the
  operator's own are left alone. The ConfigMap's `sharedTids`, which shaped them, has no effect and is still
  accepted.
- **Only what it owns:** deploymentproxy changes or deletes a namespace only if it carries its owner label for
  the same deployment; an existing namespace of the same name without it is refused, never adopted. It never
  touches a deployment namespace. Deleting a deployment namespace deletes all its tenant namespaces.

## Manifests

| | |
|---|---|
| `config/crd` | the `TenantNamespace` CRD (schema, `tid` pattern, CEL immutability rule) |
| `config/rbac` | deploymentproxy's service account, ClusterRole and binding |
| `config/manager` | its namespace, Deployment and ConfigMap (reserved tids, quota default and per-deployment overrides) |
| `config/deployment-namespace` | the Role the pipeline applies in each deployment namespace: `NAMESPACE=tac-<deployment> envsubst '$NAMESPACE' < config/deployment-namespace/operator.yaml \| kubectl apply -f -` |

Every pod template runs on arm64 nodes (the platform runs arm64 everywhere, GKE Axion): it selects
`kubernetes.io/arch: arm64` and tolerates the `kubernetes.io/arch=arm64:NoSchedule` taint GKE puts on Arm nodes,
explicitly, for a cluster that does not add the toleration itself ([#11](https://github.com/tacplus-dev/deploymentproxy/issues/11)).
A test (`manifests_test.go`) holds every pod template under `config/` to it.

## Build and test

`make check` runs the gates: gofmt, `go vet`, build, and the tests. The controller tests run a real API server
([envtest](https://book.kubebuilder.io/reference/envtest), fetched by `setup-envtest`) with the CRD, and run the
controller as deploymentproxy's service account under the RBAC manifests above. `make image` builds the container image.

## Releasing

deploymentproxy is released by version, and fleets pin a release line, `MAJOR.MINOR`, which is always that line's
highest patch ([tacplus#180](https://github.com/tacplus-dev/tacplus/issues/180)). Versions are `0.x` until
SeaBeam's launch (2026-12-01), when `1.0.0` is cut; on `0.x` a minor may break, a patch never does.

A change merged to main is released by the same run that merged it: a fix as the next patch, a feature as the
next minor. To cut one, from the commit to release:

```sh
git tag v0.1.1          # vMAJOR.MINOR.PATCH; a patch to an older line is tagged on release/MAJOR.MINOR
git push origin v0.1.1
```

A new minor also moves the image in `config/manager/deployment.yaml` to its line, in the commit it tags.

The tag runs [release.yml](.github/workflows/release.yml): the gates on the tag, then
`ghcr.io/tacplus-dev/deploymentproxy:<version>` for linux/amd64 and linux/arm64, labelled with the version and commit
(`org.opencontainers.image.version`, `.revision`), and `:<MAJOR.MINOR>` moved to it only when it is the line's
highest patch, so the line tag never goes backwards and no other line moves
([scripts/release-tags.sh](scripts/release-tags.sh)). The GitHub release's notes are the commits since the
previous release ([scripts/release-notes.sh](scripts/release-notes.sh)). A pre-release (`v0.2.0-rc.1`) publishes
only its own tag. Running the workflow by hand (`gh workflow run release.yml -f version=v0.1.1`) is a dry run:
it builds both architectures and prints the tags and notes that version would get, and publishes nothing.
