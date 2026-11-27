# History: nsproxy

Append-only: add entries at the end, never edit or remove one (CI enforces it). One line each:
`- <YYYY-MM-DD> <who: looper|agent|jody> <kind: issue|decision|release|deploy> <one line> (<links>)`

- 2026-10-28 looper issue Go floor go1.27.0: gates build on a toolchain with 10 reachable stdlib vulnerabilities; declare go 1.27.3 (#7, 6b1d613)
- 2026-10-28 looper release v0.1.3 (https://github.com/tacplus-dev/nsproxy/releases/tag/v0.1.3)
- 2026-10-28 looper issue Vulnerable golang.org/x/net v0.60.0 (GO-2026-6866, GO-2026-6860): bump to v0.62.0 (#8, 0229643)
- 2026-10-28 looper release v0.1.4 (https://github.com/tacplus-dev/nsproxy/releases/tag/v0.1.4)
- 2026-11-02 looper issue Rename nsproxy to deploymentproxy: module, API group, labels, image, docs (v0.2.0) (#9, 7f55164)
- 2026-11-02 looper release v0.2.0 (https://github.com/tacplus-dev/deploymentproxy/releases/tag/v0.2.0)
- 2026-11-03 looper issue Pods can't schedule on Axion (arm64) nodes: no arm64 nodeSelector or toleration for GKE's kubernetes.io/arch taint (#11, 30da9ef)
- 2026-11-09 looper issue Remove NetworkPolicy-based isolation from tenant namespaces: bearer tokens isolate, not reachability (#14)
- 2026-11-14 looper issue Security run fails the policy: gosec G404 (HIGH) on PodFault's math/rand pod pick, internal/controller/podfault.go:396 (#20, ac89260)
- 2026-11-14 looper issue Gates flake: TestPodFaultSpecImmutable gets Conflict, not Invalid, when the controller writes the PodFault first (#18, 8c85af2)
- 2026-11-15 looper issue Go floor go1.27.3: GO-2026-6859 (net/http MaxBytesReader race) is reachable; declare go 1.27.4 (#17)
- 2026-11-15 looper issue Pin the build: a committed build.lock naming this repository's build image by digest (#13, 3bac8ad)
- 2026-11-17 looper issue ClusterRole's cluster-wide delete on networkpolicies (kept only for #14's retired policies) fails the security run: trivy KSV-0056 (HIGH) (#24)
- 2026-11-17 looper issue Dockerfile build stage golang:1.27 (Debian 13.7) fails the security run: 182 HIGH/CRITICAL package advisories (7 failing the policy); build on golang:1.27-alpine (#23)
- 2026-11-18 looper issue Security run, below the bar: zlib CVE-2026-4738 in the build stage, and six trivy manifest/Dockerfile checks (KSV-0048, KSV-0125, KSV-0011, KSV-0020, KSV-0021, DS-0026) with no exception (#27)
- 2026-11-22 looper issue Pinned images behind their lines: relock (build.lock: ghcr.io/tacplus-dev/tacplus/build:2.6.8 → ghcr.io/tacplus-dev/tacplus/build:2.6.13) (#30)
- 2026-11-23 looper issue Pinned images behind their lines: relock (build.lock: ghcr.io/tacplus-dev/tacplus/build:2.6.13 → ghcr.io/tacplus-dev/tacplus/build:2.6.14) (#31)
- 2026-11-24 looper issue Release image base pinned by digest, not the floating tag (#32)
- 2026-11-25 looper issue Pinned images behind their lines: relock (build.lock: ghcr.io/tacplus-dev/tacplus/build:2.6.14 → ghcr.io/tacplus-dev/tacplus/build:20261123-57a6521) (#34)
