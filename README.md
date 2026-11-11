# deploymentproxy

The one component per cluster that holds cluster privilege on a deployment's behalf. It creates and deletes
**tenant namespaces** for the deployments on a Kubernetes cluster, and applies **chaos faults** to a
deployment's own pods, so that a deployment needs no privilege outside its own namespace. It was formerly nsproxy, renamed in v0.2.0
([decision](https://github.com/tacplus-dev/roadmap/blob/main/decisions/deploymentproxy-2026-11-02.md)).

Several deployments share one cluster. The top pipeline creates each deployment's namespace,
`tac-<deployment>`, and deploys deploymentproxy once per cluster. A deployment's operator, running in
`tac-<deployment>`, asks deploymentproxy to create or delete its tenants' namespaces, `tac-<deployment>-<tid>`
(a tid is 8 characters of `[0-9a-z]`; all-digit ids are reserved, `people` is `00000001`, `restore` is
`00000002`). deploymentproxy is the only thing holding the cluster-wide permission, and it uses it narrowly.

Work arrives as issues; see github.com/tacplus-dev/looper.

## How it works

deploymentproxy is a Kubernetes controller for custom resources of `deploymentproxy.tacplus.dev/v1`:
`TenantNamespace` and the chaos fault kinds ([below](#chaos-faults)). It has no network listener. The
operator creates a `TenantNamespace` in its own namespace:

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

## Chaos faults

A deployment's chaos controller ([tacplus-dev/chaos](https://github.com/tacplus-dev/chaos)) asks deploymentproxy
for a fault by creating a custom resource in its own deployment namespace, one kind per fault family; deploymentproxy
applies it, reverts it, and writes the outcome to its status only. The chaos controller records that outcome in
the event trace ([decision](https://github.com/tacplus-dev/roadmap/blob/main/decisions/deploymentproxy-2026-11-02.md)).

| Kind | Actions | Done by |
|---|---|---|
| `PodFault` | `Kill`, `Failure` | the deploymentproxy controller, through the Kubernetes API |

Node faults (network delay and loss, clock skew, stress) come with the node agent, a separate privileged
DaemonSet ([#10](https://github.com/tacplus-dev/deploymentproxy/issues/10)); their kinds follow the fault list of
[tacplus-dev/chaos#1](https://github.com/tacplus-dev/chaos/issues/1).

### PodFault

```yaml
apiVersion: deploymentproxy.tacplus.dev/v1
kind: PodFault
metadata:
  name: round-7
  namespace: tac-prod
spec:
  tag: "fleet=prod,round=7,fault=3"   # opaque, copied unchanged to status.tag
  action: Kill                        # or Failure
  target:
    namespace: tac-prod-a1b2c3d4      # default: the PodFault's own namespace
    selector:                         # or pod: <name>; exactly one of the two
      matchLabels: {app.kubernetes.io/name: stratum}
  duration: 30s
  gracePeriodSeconds: 0               # Kill only
```

- **Kill** deletes the pod, once, with its grace period; its owner recreates it. Nothing is put back: at the end
  of `duration` the fault is marked reverted.
- **Failure** sets every container of the pod (not its init containers) to the failure image (`chaos.failureImage`
  in the ConfigMap, default `registry.k8s.io/pause:3.10`), so the pod stays scheduled but stops serving; at the
  end of `duration`, or when the PodFault is deleted, the original images are put back. The pod and its original
  images are recorded in the PodFault's status before the pod is touched, and the revert uses that record only
  (never anything on the pod, which others may write), so it survives a deploymentproxy restart. A
  `deploymentproxy.tacplus.dev/revert` finalizer holds the PodFault until its pod is put back. While held, the pod
  carries the `deploymentproxy.tacplus.dev/pod-fault` label (the PodFault's UID), so no other fault takes it.
- **A selector** chooses one pod at random among the pods it matches that the fault may target; it never chooses an
  excluded one.
- **The spec is immutable**, and a fault is applied at most once, to the pod first chosen (a retry after an API
  error acts on that pod again, never another): a refused PodFault stays refused. Create a new one.

**Bounds** (checked by deploymentproxy, the side that acts; out of bound is refused, never clamped):

| Parameter | Bound |
|---|---|
| `duration` | a Go duration, 1s to 5m, required |
| `gracePeriodSeconds` | 0 to 30, default 0; `Kill` only |
| `target` | exactly one of `pod` and a non-empty `selector`; one pod per PodFault |
| `tag` | at most 1024 characters (the CRD rejects a longer one, and an `action` other than `Kill` and `Failure`) |
| one pod | at most one `Failure` at a time |

**Status:** `tag` (`spec.tag`, unchanged), `namespace`, `pod` and `podUID` (the target), `originalImages` (a
Failure's), `startTime`, `endTime`, and two conditions:

- `Applied`: `True` (reason `Applied`) once the fault is applied; `Unknown` (reason `Applying`) between choosing
  the pod and acting on it; `False` with the refusal as its reason; `False` with reason `Error` while an API error
  is retried.
- `Reverted`: `False` (reason `Active`) while the fault lasts; `True` with reason `Expired` at its end (a Failure's
  images are put back), or `TargetGone` when the pod a Failure held was deleted or replaced meanwhile.

**Refusal reasons** (`Applied=False`):

| Reason | When |
|---|---|
| `NotDeploymentNamespace` | the PodFault's namespace has no `tacplus.dev/deployment` label |
| `InTenantNamespace` | the PodFault is in a tenant namespace; ask from the deployment namespace |
| `InvalidDeployment` | the deployment label is not 1-16 characters of `[a-z0-9-]` starting with a letter |
| `ExcludedNamespace` | the target namespace is excluded: `kube-*`, `default`, `deploymentproxy`, `letsencrypt`, `cert-manager`, or one in `chaos.excludedNamespaces` |
| `OtherDeployment` | the target namespace belongs to another deployment: another deployment namespace (whatever its label says, only the requester's own is its), or a tenant namespace not made for this deployment namespace |
| `UnlabelledNamespace` | the target namespace has neither `tacplus.dev/deployment` nor `deploymentproxy.tacplus.dev/deployment` |
| `TargetNotFound` | the namespace or the pod does not exist, is being deleted, or the selector matches no pod |
| `ExcludedPod` | the pod's `app.kubernetes.io/name` is excluded: `letsencrypt`, `cert-manager`, `chaos-controller`, or one in `chaos.excludedServices` |
| `SecretMaterial` | the pod mounts secret material: a `secret` volume, a `projected` one with a Secret source, or a `csi` volume |
| `TargetBusy` | the pod is already held by another PodFault's `Failure` |
| `OutOfBounds` | a parameter is out of its bound (above) |
| `InvalidSpec` | `duration` is not a duration, `gracePeriodSeconds` is set on a `Failure`, or the target is not exactly one of `pod` and a valid, non-empty `selector` |

The scope is the same as `TenantNamespace`'s: the deployment comes from the PodFault's namespace's
`tacplus.dev/deployment` label, never from a name, and a fault may target only that namespace and the tenant
namespaces deploymentproxy made for it. **Who may ask** is the Role in
[config/deployment-namespace/chaos.yaml](config/deployment-namespace/chaos.yaml): the deployment's
`chaos-controller` service account may create, read and delete `PodFault` objects in its namespace, and has no
right on pods.

## Manifests

| | |
|---|---|
| `config/crd` | the `TenantNamespace` CRD (schema, `tid` pattern, CEL immutability rule) and the `PodFault` CRD |
| `config/rbac` | deploymentproxy's service account, ClusterRole and binding |
| `config/manager` | its namespace, Deployment and ConfigMap (reserved tids, quota default and per-deployment overrides, chaos exclusions and failure image) |
| `config/deployment-namespace` | the Roles the pipeline applies in each deployment namespace: `operator.yaml` (`TenantNamespace`, for `operator`) and `chaos.yaml` (`PodFault`, for `chaos-controller`), each with `NAMESPACE=tac-<deployment> envsubst '$NAMESPACE' < config/deployment-namespace/<file> \| kubectl apply -f -` |

Every pod template runs on arm64 nodes (the platform runs arm64 everywhere, GKE Axion): it selects
`kubernetes.io/arch: arm64` and tolerates the `kubernetes.io/arch=arm64:NoSchedule` taint GKE puts on Arm nodes,
explicitly, for a cluster that does not add the toleration itself ([#11](https://github.com/tacplus-dev/deploymentproxy/issues/11)).
A test (`manifests_test.go`) holds every pod template under `config/` to it.

## Build and test

`make check` runs the gates: gofmt, `go vet`, build, and the tests. The controller tests run a real API server
([envtest](https://book.kubebuilder.io/reference/envtest), fetched by `setup-envtest`) with the CRD, and run the
controllers as deploymentproxy's service account under the RBAC manifests above. envtest runs no kubelet, so
the PodFault tests check the API side (pods deleted, images patched and put back), not containers restarting. `make image` builds the container image.

The build is pinned: `build.lock` names the build image by digest (tacplus build 2.6.8), and the build pipeline
([tacplus-dev/looper#71](https://github.com/tacplus-dev/looper/issues/71)) runs `just gates` (`make check`) in it,
without Docker, with its security checks. To move the pin, change the digest in `build.lock` to the new image's
(`docker buildx imagetools inspect <image>:<tag>` prints it): a change like any other, landed on the full gates.

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
