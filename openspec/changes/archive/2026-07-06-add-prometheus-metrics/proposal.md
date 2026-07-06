## Why

The plugin already computes rich per-device health (temperature threshold + ARC
heartbeat, recovered every 30s) and reports it to the kubelet via `ListAndWatch`.
But that signal is invisible to operators — the only way to see *why* a device is
unhealthy today is `kubectl logs`. Every mature vendor plugin exposes Prometheus
metrics (NVIDIA DCGM, AMD's exporter); it is the Phase-1 gap in
[ROADMAP.md](../../../ROADMAP.md) and the main thing missing for production
observability. A `/metrics` endpoint makes device health, temperature, and
allocation state visible to dashboards and alerts.

## What Changes

- Serve an HTTP **`/metrics`** endpoint (Prometheus text format) on a configurable
  port (default `9102`), started once in `cmd/tt-device-plugin`.
- Introduce a Prometheus **collector** that, on each scrape, reports:
  - per device (labels: `class`, `device_id`): `tt_device_health` (1/0),
    `tt_device_temperature_celsius`, `tt_device_temperature_max_celsius`
  - per class (label: `class`): `tt_devices_total`, `tt_allocations_total`
  - default Go/process metrics from the client library.
- Add the `prometheus/client_golang` dependency.
- Config: `TT_METRICS_PORT` env var and Helm values (`metrics.enabled`,
  `metrics.port`); expose the container port on the DaemonSet.
- Preserve the non-privileged security context — the change only binds a TCP
  port, no new host access or disk writes.

## Capabilities

### New Capabilities
- `device-metrics`: exposing Tenstorrent device health, temperature, and
  allocation state as Prometheus metrics over an HTTP `/metrics` endpoint.

### Modified Capabilities
- (none — `openspec/specs/` is currently empty; existing health and allocation
  behavior is unchanged, only *observed*.)

## Impact

- **Code:** `cmd/tt-device-plugin` (start the metrics server), a new
  `internal/metrics` package (the collector), and a small read-only hook in
  `internal/plugin` so the collector can enumerate devices + current health and
  read the allocation counter.
- **Dependency:** `github.com/prometheus/client_golang` (+ transitive deps).
- **Deployment:** Helm chart gains `metrics.enabled` / `metrics.port` and a
  DaemonSet `containerPort`.
- **Security:** posture unchanged; adds one listening port (default `9102`).

## Non-goals

- No Prometheus Operator `ServiceMonitor` or scrape config — that belongs to the
  future operator phase.
- No alerting rules or Grafana dashboards.
- No change to the health logic or the 30s `ListAndWatch` cadence.
- No auth/TLS on the endpoint (cluster-internal scrape only).
