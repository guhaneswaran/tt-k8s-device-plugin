# Prerequisites & Version Constraints

Everything required to build, deploy, and run the Tenstorrent Kubernetes device
plugin, plus the hard version constraints for CDI mode. Two deployment modes
exist and they have **different** requirements:

- **Legacy mode** (default) — plugin returns raw device nodes + mounts in `Allocate`.
- **CDI mode** (`cdi.enabled=true` / `TT_CDI_ENABLED=true`) — plugin returns CDI
  device names; the container runtime injects from a CDI spec.

---

## 1. Host (node) prerequisites

| Requirement | Needed value | Notes |
|-------------|--------------|-------|
| Tenstorrent card | e.g. n150 (Wormhole) | Discovered from `/dev/tenstorrent/*` |
| `tt-kmd` kernel driver | loaded (`tenstorrent` module) | Provides `/dev/tenstorrent/N` (char major 240) and `/sys/class/tenstorrent/tenstorrent!N` |
| sysfs entries | `tt_card_type`, `tt_heartbeat`, `device/numa_node`, `device/hwmon/hwmon*` | Discovery + health depend on these being readable |
| `/dev/hugepages-1G` | optional | Mounted into workloads if present |

The card + sysfs must be visible **inside the node** (for minikube docker
driver, they are passed through automatically — verify with
`docker exec minikube ls /dev/tenstorrent`).

---

## 2. Kubernetes cluster requirements

| Component | Legacy mode | CDI mode |
|-----------|-------------|----------|
| Kubernetes version | ≥ 1.31 (chart floor) | **≥ 1.31** (CDI feature gate GA) |
| Container runtime | any (docker/cri-dockerd, containerd, CRI-O) | **containerd ≥ 1.7 or CRI-O** — docker/cri-dockerd will NOT inject CDI devices |
| Runtime CDI enabled | n/a | `enable_cdi = true`, `cdi_spec_dirs` include `/var/run/cdi` (default in containerd 2.x) |
| Feature gate `DevicePluginCDIDevices` | n/a | GA since 1.31 (on by default) |

Note: the Helm chart declares `kubeVersion: ">= 1.31.0-0"`. The plugin's
**legacy** path itself works from 1.18, but the chart floor is set to 1.31 to
match CDI availability. Even so, enabling `cdi.enabled=true` on a
docker/cri-dockerd runtime results in containers that start with **no device**
(the runtime silently ignores CDI names) — the K8s version alone is not enough.

Optional (health visibility, not required):

| Feature gate | Version | Effect |
|--------------|---------|--------|
| `ResourceHealthStatus` | beta, default-on since 1.36 | Surfaces per-device health in `pod.status.allocatedResourcesStatus` |

---

## 3. CDI specification constraints

Enforced by `internal/cdi`; do not violate these when editing spec generation:

- **`cdiVersion` MUST be ≥ 0.6.0.** We emit `0.6.0`. Rationale: device names are
  digits (`"0"`, `"1"` — requires ≥ 0.5.0) and `kind` is dotted
  (`tenstorrent.com/n150` — requires ≥ 0.6.0). Do not lower it.
- **`kind`** = `prefix/name` where prefix is a DNS subdomain
  (`tenstorrent.com/n150`); equals the device plugin resource name.
- **Fully-qualified device name** = `vendor/class=name` (`tenstorrent.com/n150=0`).
- **Host-directory mounts MUST carry an explicit `rbind` (or `bind`) option.**
  Without it, runc fails with `mount ... no such device` (hit on `/sys`).
- **Spec location**: `/var/run/cdi/tenstorrent-<class>.json` — must be within the
  runtime's `cdi_spec_dirs`.
- No `cdi.k8s.io/*` pod annotation is expected with device-plugin CDI when the
  runtime supports the CRI `CDIDevices` field directly (containerd 2.x does).
  Its absence is correct, not a bug.

---

## 4. Build & dev toolchain

| Tool | Version (pinned/verified) | Where |
|------|---------------------------|-------|
| Go (module) | `go 1.25.0` | `go.mod` |
| Go (build image) | `golang:1.25-alpine` | `Dockerfile` |
| `k8s.io/kubelet` (device plugin API) | `v0.35.3` | `go.mod` |
| `google.golang.org/grpc` | `v1.79.3` | `go.mod` |
| `github.com/fsnotify/fsnotify` | `v1.9.0` | `go.mod` |
| Helm | v3+ (chart `apiVersion: v2`); verified with v4.2.2 | host |
| Docker | for `docker build` + `minikube image load` | host |
| minikube | verified v1.38.1 | host |
| kubectl | matching cluster (v1.35.x) | host |

Dev environment currently: minikube on the **docker driver** with the
**containerd runtime** (persisted via `minikube config set container-runtime
containerd`), Kubernetes v1.35.1, containerd 2.2.1 (CDI on by default).

---

## 5. Deployment requirements (DaemonSet)

Required host mounts (present in `deploy/daemonset.yaml` and the Helm chart):

| Volume | Host path | Mode | Purpose |
|--------|-----------|------|---------|
| device-plugins | `/var/lib/kubelet/device-plugins` | rw | kubelet registration + plugin socket |
| sys | `/sys` | ro | sysfs discovery + health |
| dev-tenstorrent | `/dev/tenstorrent` | ro | device enumeration |
| hugepages | `/dev/hugepages-1G` | ro | optional 1G hugepages |
| **cdi** (CDI mode only) | `/var/run/cdi` | **rw** | where the plugin writes CDI specs |

Security context (intentionally non-privileged): `allowPrivilegeEscalation:
false`, `readOnlyRootFilesystem: true`, `capabilities.drop: ["ALL"]`. Do not
switch to privileged.

Hardcoded kubelet socket paths (not configurable):
- Registration: `/var/lib/kubelet/device-plugins/kubelet.sock`
- Plugin socket: `/var/lib/kubelet/device-plugins/tenstorrent-<class>.sock`

---

## 6. Quick constraint summary

- Legacy mode: plugin works on Kubernetes ≥ 1.18, any runtime (but the Helm chart floor is 1.31).
- **CDI mode: Kubernetes ≥ 1.31 AND containerd ≥ 1.7 / CRI-O with CDI enabled.**
- CDI spec `cdiVersion` ≥ 0.6.0; mounts need `rbind`; specs in `/var/run/cdi`.
- Go 1.25 to build; device plugin API `k8s.io/kubelet v0.35.3`.
- Card + sysfs must be visible inside the node; `tt-kmd` loaded on the host.
