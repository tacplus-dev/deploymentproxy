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
