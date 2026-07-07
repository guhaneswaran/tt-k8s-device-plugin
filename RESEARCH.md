# Research Notes — Kubernetes Device Plugins, CDI, DRA, and Vendor Landscape

Consolidated findings behind this project's design and [ROADMAP.md](ROADMAP.md),
so the research doesn't have to be repeated. Every non-obvious claim is sourced.
Last updated: 2026-07-02.

- [1. The device plugin model](#1-the-device-plugin-model)
- [2. CDI (Container Device Interface)](#2-cdi-container-device-interface)
- [3. DRA (Dynamic Resource Allocation) — the successor](#3-dra-dynamic-resource-allocation--the-successor)
- [4. Vendor plugin comparison](#4-vendor-plugin-comparison)
- [5. Version & compatibility matrix](#5-version--compatibility-matrix)
- [6. Decisions taken for the TT plugin](#6-decisions-taken-for-the-tt-plugin)
- [7. Sources](#7-sources)

---

## 1. The device plugin model

A device plugin is a gRPC server on a Unix socket that advertises vendor hardware
to the kubelet as an extended resource (`vendor-domain/resourcetype`, e.g.
`tenstorrent.com/n150`). Kubernetes core is untouched.

**Sockets (hardcoded, not configurable):**
- Registration: `/var/lib/kubelet/device-plugins/kubelet.sock`
- Plugin: `/var/lib/kubelet/device-plugins/<plugin>.sock`

**Registration handshake (order matters):** initialize → **start gRPC server
first** → `Register(RegisterRequest)` on `kubelet.sock` with socket name, API
version, ResourceName → kubelet advertises healthy device count.

**gRPC methods (KEP-3573):**

| Method | Required | Purpose |
|--------|:--------:|---------|
| `GetDevicePluginOptions` | Yes | Advertise which optional methods are supported |
| `ListAndWatch` | Yes | **Stream** the device list; re-send on any health change |
| `Allocate` | Yes | Return device nodes / mounts / envs (or CDI names) for containers |
| `GetPreferredAllocation` | Optional | Topology-aware allocation hints |
| `PreStartContainer` | Optional | Device-specific setup before container start |

**Health semantics:** failures are reported **only** via the `ListAndWatch`
stream by flipping a device's `Health` field to `Unhealthy`. Effect: **allocatable
count drops; capacity stays the same**; pods already on a failed device keep
running (may crash-loop). The `Device` message has only three fields — `ID`,
`Health` (string `Healthy`/`Unhealthy`), `Topology` — **there is no per-device
health message field**. Richer per-device health surfaces via the kubelet's
`ResourceHealthStatus` feature (`pod.status.allocatedResourcesStatus`, beta /
default-on since ~v1.36), which the kubelet synthesizes; the plugin's only lever
is the `Health` string.

**Kubelet restart:** on startup the kubelet deletes every socket under
`/var/lib/kubelet/device-plugins/`; the plugin must watch for its socket
disappearing (or `kubelet.sock` reappearing) and re-register.

**Feature gate:** `DevicePlugins` graduated to GA in v1.10; the API has been
stable since. Device plugins are **not deprecated**.

**Constraints:** devices are integer-only resources — no fractional requests, no
overcommit, not shareable between containers (without CDI/DRA sharing features).

---

## 2. CDI (Container Device Interface)

CDI lets a vendor describe container modifications for a device in a spec file
that a CDI-aware runtime injects. It is a CNCF project, consumed by containerd,
CRI-O, Docker (v25+), and Podman.

**KEP-4009** added CDI to the device plugin API: a `CDIDevice{ name }` message and
a `cdi_devices` (field 5) repeated field on `ContainerAllocateResponse`. Feature
gate `DevicePluginCDIDevices` — alpha 1.28, beta/default-on 1.29, **GA 1.31**.
Opt-in and backward compatible; older plugins are unaffected.

**Key behavioral fact:** when a device plugin returns `CdiDevices`, the kubelet
passes them to the runtime via the **CRI `CDIDevices` field** — **no
`cdi.k8s.io/*` pod annotation appears** when the runtime supports that field
directly (containerd 2.x does). Annotations are only a fallback for older
runtimes. Absence of the annotation is correct, not a bug.

**CDI spec conformance (enforced by `internal/cdi`, do not violate):**
- `cdiVersion` **MUST be ≥ 0.6.0**. We emit `0.6.0`. Rationale: digit device
  names (`"0"`, `"1"`) require ≥0.5.0, and a dotted `kind`
  (`tenstorrent.com/n150`) requires ≥0.6.0.
- `kind` = `prefix/name`, prefix a DNS subdomain; equals the resource name.
- Fully-qualified device name = `vendor/class=name` (`tenstorrent.com/n150=0`).
- **Host-directory mounts MUST carry an explicit `rbind` (or `bind`) option** —
  otherwise runc fails `mount ... no such device` (learned the hard way on
  `/sys`). The legacy `pluginapi.Mount` got bind semantics implicitly from
  containerd's CRI layer; CDI needs it spelled out.
- Spec location: `/var/run/cdi/tenstorrent-<class>.json`, within the runtime's
  `cdi_spec_dirs` (`/etc/cdi`, `/var/run/cdi` by default).

**Does CDI provide benefits today?** For basic "give the container this device
node + mounts + env," **no** — CDI and the legacy path produce a byte-for-byte
identical container (verified). CDI currently costs *more* (a spec file, a
CDI-capable runtime, the `rbind` gotcha). Its value is future-facing:
- **Lifecycle hooks** (`createRuntime`/`createContainer`/`startContainer`) — run a
  binary at container setup (e.g. device reset, library injection). The device
  plugin API has **no field** for this.
- **Ecosystem portability** — the same spec works under containerd, CRI-O,
  Docker, Podman; the device plugin API is kubelet-only.
- **It is the injection mechanism for DRA** (see §3) — so CDI is the bridge
  forward, not throwaway.

Intel's own docs state CDI "does not yet provide any benefits compared to the
traditional Kubernetes Device Plugin API" — matching this assessment.

---

## 3. DRA (Dynamic Resource Allocation) — the successor

**The most important strategic finding.** DRA is the structured-parameter
successor to the count-based device plugin model: the scheduler makes allocation
decisions from device *attributes* exposed by a vendor DRA driver, instead of
opaque integer counts.

- **DRA core graduated to GA in Kubernetes v1.34** (released ~Aug/Sept 2025),
  **enabled by default**; further updates landed in v1.36. (One secondary source
  cited v1.35 for "stable" — v1.34 GA is the best-supported, from the project's
  own release blog/docs.)
- Device plugins are **not deprecated**; the two coexist. But the ecosystem
  (e.g. Google, KubeCon EU 2026 messaging) frames device plugins as legacy and
  steers new work toward DRA. No deprecation is mandated — the device plugin path
  is safe for the foreseeable future.
- **CDI is DRA's injection mechanism** — a DRA driver expresses container edits as
  CDI. The CDI work in this repo is directly reusable for a future DRA driver.
- **NVIDIA and AMD already ship DRA drivers.** AMD's requires K8s 1.32+, CDI
  enabled (containerd 2.0+), and does topology-aware partition scheduling.
- **KEP-5004 `DRAExtendedResource`** (1.34) lets users request DRA-backed
  resources via the classic extended-resource syntax (e.g.
  `tenstorrent.com/n150: 1`) — a migration bridge so a DRA driver can serve
  old-style requests.

**Implication:** keep the device plugin as the compatibility baseline (works on
all cluster versions; DRA needs ≥1.34). Build device **sharing/partitioning** on a
DRA driver, **not** device-plugin time-slicing (which DRA supersedes). Gate DRA
adoption on the minimum K8s version the TT plugin must support.

---

## 4. Vendor plugin comparison

Legend: ✅ full · 🟡 partial/experimental · ❌ none.

| Feature | NVIDIA | AMD (ROCm) | Intel | TT (this repo) |
|---------|:------:|:----------:|:-----:|:--------------:|
| Core API | ✅ | ✅ | ✅ | ✅ |
| Startup discovery | ✅ | ✅ | ✅ | ✅ |
| Re-discover on kubelet restart | ✅ | ✅ | ✅ | ✅ |
| Runtime hot-plug | ❌ | ❌ | ❌ | ❌ |
| Health → unhealthy | ✅ (NVML XID) | 🟡 exp | 🟡 min | ✅ (temp+heartbeat) |
| Health **auto-recovery** | ❌ | 🟡 | 🟡 | ✅ (30s re-eval) |
| Topology / NUMA | ✅ | 🟡 | ✅ | ✅ |
| CDI | ✅ opt-in | ❌ | ✅ opt-in | ✅ opt-in |
| Non-privileged / hardened | 🟡 | ❌ (needs /dev/kfd) | 🟡 | ✅ |
| Sharing: time-slicing | ✅ | 🟡 | ✅ | ❌ |
| Sharing: spatial (MPS) | ✅ | 🟡 | 🟡 | ❌ |
| Hardware partitioning | ✅ MIG | 🟡 (MI300) | 🟡 (SR-IOV) | ❌ (hw-dependent) |
| NFD / node labeling | ✅ | ✅ | ✅ | ❌ |
| Prometheus metrics | ✅ (DCGM) | ✅ (exporter) | 🟡 | 🟡 (basic, in-plugin) |
| Operator | ✅ (GPU Operator) | 🟡 | ✅ | ❌ |
| DRA driver | ✅ | ✅ | 🟡 | ❌ |

**Notable per-vendor facts:**
- **NVIDIA** health checks NVML for XID critical events → unhealthy, but has a
  documented weakness: a GPU that recovers from an XID error **is not re-fetched**
  and stays unhealthy (issues #1014, gpu-operator #1065). TT's 30s re-evaluation
  avoids this.
- **AMD** health is experimental, needs privileged `/dev/kfd` and a
  metrics-exporter gRPC socket; no CDI.
- **Intel** uses an operator with per-device CRDs across many accelerators
  (GPU/FPGA/QAT/…); GPU plugin supports CDI but calls it no-benefit-yet.
- **Hot-plug: none of the three do runtime add/remove** — startup + kubelet-restart
  re-discovery is the industry norm.

### 4a. Observability: who owns "free capacity" vs. telemetry

A recurring confusion: the device plugin **cannot** report how many devices are
*free*. The device-plugin gRPC API (`Register`, `ListAndWatch`, `Allocate`,
`GetPreferredAllocation`, `PreStartContainer`) has **no release/`Deallocate`
callback** — kubelet tells the plugin when a device is handed out but never when a
pod exits and returns it. So the plugin sees allocations flow out and never sees
them come back; it has no accurate "in-use / free" state to expose. This is a
Kubernetes API constraint, so it applies identically to NVIDIA, AMD, Intel, and us.

Kubelet owns the allocation ledger. Therefore **free capacity is a Kubernetes
question, not a plugin metric**, observed the same way for every vendor:

```
free = allocatable − sum(requests of running pods)
```

- **kubectl:** `kubectl describe node <n> | grep -A6 "Allocated resources"` —
  compare `Allocatable` vs `Requests` for `tenstorrent.com/n150`.
- **Prometheus (fleet):** via **kube-state-metrics** (reads the API server, not the
  plugin):
  ```promql
  kube_node_status_allocatable{resource="tenstorrent_com_n150"}
    - on(node) sum by (node) (
        kube_pod_container_resource_requests{resource="tenstorrent_com_n150"}
      )
  ```

Consequently, our `tt_allocations_total` is a **counter** (cumulative allocation
*churn*, read via `rate()`), not a free-capacity gauge — it answers "how much
allocation activity," not "how many are free." Those are different questions.

**Telemetry is a separate component across all three vendors.** None fold rich
device metrics into the device plugin; each ships a dedicated exporter:

| Vendor | Allocation (device plugin) | Telemetry (separate exporter) |
|--------|----------------------------|-------------------------------|
| NVIDIA | `k8s-device-plugin`        | **DCGM-exporter** (util, mem, power, ECC, temp) |
| AMD    | ROCm device plugin         | **device-metrics-exporter** (gRPC socket) |
| Intel  | GPU plugin (operator)      | **xpumanager** exporter |
| TT     | `tt-device-plugin`         | *basic health/temp bundled inline (this project)* |

We deliberately fold **basic** health + temperature + allocation-churn into the
plugin (one binary, single-card scope). The vendor-aligned evolution is to split
telemetry into a `tt-metrics-exporter` (DCGM-style) once real utilization / power /
memory metrics are wanted, keeping the plugin lean and single-purpose. "Free cards"
stays a kube-state-metrics query regardless — this is a validated design choice, not
a gap.

---

## 5. Version & compatibility matrix

| Concern | Legacy device-plugin path | CDI mode | DRA |
|---------|---------------------------|----------|-----|
| Kubernetes | ≥ 1.18 (plugin); chart floor 1.31 | ≥ 1.31 (GA gate) | ≥ 1.34 (GA) |
| Runtime | any | containerd ≥1.7 / CRI-O, CDI enabled | containerd 2.0+ w/ CDI |
| CDI spec | n/a | `cdiVersion` ≥ 0.6.0 | via driver |

Dev environment of record: minikube (docker driver, **containerd 2.2.1** runtime,
CDI on by default), Kubernetes v1.35.1, Go 1.25 build, `k8s.io/kubelet v0.35.3`.

---

## 6. Decisions taken for the TT plugin

So these aren't re-litigated:

1. **CDI = opt-in, default off** — aligns with NVIDIA/Intel; legacy path is the
   default and works on all runtimes. (Done, validated.)
2. **Health includes a temperature threshold with auto-recovery** — over-temp card
   → unhealthy → recovers when it cools. Better than NVIDIA's non-recovery. (Done.)
3. **Non-privileged security context** — deliberate divergence from the vendor
   blogs' "run privileged" advice; keep it hardened. (Done.)
4. **No hot-plug** — no vendor does it; startup + kubelet-restart re-discovery is
   enough. (Skip.)
5. **No device-plugin time-slicing** — DRA supersedes it; do sharing via a DRA
   driver later. (Skip / defer to DRA.)
6. **NFD + operator = later phase** — standalone plugin stays lean; bundle NFD in a
   future TT operator (NVIDIA-style).
7. **DRA = strategic track** — start a DRA driver when supported K8s versions reach
   ≥1.34; it reuses the CDI specs. Gate on the minimum K8s version TT must support.
8. **Chart `kubeVersion` = ≥1.31** — matches CDI GA availability.
9. **No `RuntimeClass`** — unlike NVIDIA's `handler: nvidia`, we do not ship a
   RuntimeClass. NVIDIA needs one because their injection routes pods through a
   custom `nvidia-container-runtime` wrapper. We inject via the `Allocate`
   response (plain `runc`) or via **CDI**, which standard containerd (≥1.7)
   resolves natively — no custom runtime handler. A RuntimeClass whose `handler`
   has no matching containerd `runtimes.*` entry would break pods
   (`RunContainerError`). RuntimeClass remains orthogonal — it selects an
   *isolation* runtime (Kata/gVisor); CDI still injects within whatever runtime.
   The only runtime prerequisite is `enable_cdi = true`, handled by the Phase-3
   "container-runtime integration" work, not a RuntimeClass. NVIDIA is itself
   migrating toward CDI, so this is the modern path, not a gap.

Open hardware questions to resolve before the relevant phase:
- Does TT silicon support partitioning (MIG-like) or tolerate oversubscription?
  (Decides whether device sharing is worth building at all.)
- Does TT sysfs expose error counters (ECC/PCIe) worth adding to health?

---

## 7. Sources

**Kubernetes / specs:**
- Device plugins: https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/
- KEP-3573 (device plugin): https://github.com/kubernetes/enhancements/tree/master/keps/sig-node/3573-device-plugin
- KEP-4009 (CDI in device plugin API): https://github.com/kubernetes/enhancements/tree/master/keps/sig-node/4009-add-cdi-devices-to-device-plugin-api
- CDI spec: https://github.com/cncf-tags/container-device-interface

**DRA:**
- K8s v1.34 DRA updates: https://kubernetes.io/blog/2025/09/01/kubernetes-v1-34-dra-updates/
- DRA docs: https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/
- K8s v1.36 DRA updates: https://kubernetes.io/blog/2026/05/07/kubernetes-v1-36-dra-136-updates/
- Google Cloud DRA overview: https://cloud.google.com/blog/products/containers-kubernetes/kubernetes-device-management-with-dra-dynamic-resource-allocation

**Vendor plugins:**
- NVIDIA: https://github.com/NVIDIA/k8s-device-plugin — health/recovery issues #1014, gpu-operator #1065
- AMD ROCm: https://github.com/ROCm/k8s-device-plugin
- Intel: https://github.com/intel/intel-device-plugins-for-kubernetes — GPU/CDI: https://intel.github.io/intel-device-plugins-for-kubernetes/cmd/gpu_plugin/README.html

**Background reading:**
- OneUptime — device plugins: https://oneuptime.com/blog/post/2026-01-30-kubernetes-device-plugins/view
- OneUptime — custom hardware: https://oneuptime.com/blog/post/2026-02-09-device-plugin-custom-hardware-kubernetes/view
- Overview (device plugin, CDI, NFD, GPU Operator): https://medium.com/@rifewang/overview-of-kubernetes-gpu-scheduling-device-plugin-cdi-nfd-and-gpu-operator-48a7c4213b28
