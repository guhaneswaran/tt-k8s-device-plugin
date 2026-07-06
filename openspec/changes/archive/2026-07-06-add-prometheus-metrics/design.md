## Context

The plugin process runs one `plugin.Plugin` **per resource class** (e.g. one for
`n150`), each in its own goroutine, and the set is **rebuilt on kubelet restart**
(`startPlugins` runs again). Health is computed inside `checkHealth` during each
`ListAndWatch` tick (every 30s) and is **stateful**: heartbeat-stall detection
compares the current `tt_heartbeat` read against the previous one stored in
`lastHeartbeats`. Any metrics design must not corrupt that state or duplicate it.

## Goals / Non-Goals

**Goals:**
- Expose per-device health/temperature and per-class device/allocation counts on
  a `/metrics` endpoint, consistent with what the kubelet sees.
- One metrics server for the whole process, covering all classes, surviving
  kubelet-restart plugin re-creation.
- Keep the non-privileged posture; add only a listening port.

**Non-Goals:**
- ServiceMonitor / scrape config / dashboards / alerts (operator phase).
- Changing health logic or the 30s cadence.

## Decisions

### 1. Pull-based custom Collector reading a cached snapshot — not re-running `checkHealth`
Implement `prometheus.Collector` whose `Collect()` reads a **snapshot the plugin
already cached on its last health tick** (health + last temperature per device),
rather than calling `checkHealth` again on scrape.

- *Why:* `checkHealth` mutates `lastHeartbeats` and drives stall detection at a
  fixed 30s cadence. If a scrape (Prometheus default ~15s) also called it, it
  would interleave heartbeat reads and break the "two equal consecutive reads =
  stalled" invariant, plus duplicate logging. Reading a cached snapshot makes the
  metrics **exactly match the kubelet's view** and keeps health computation in
  one place.
- *Alternative rejected — push gauges from the tick:* have `ListAndWatch` write
  into `GaugeVec`s. Rejected: couples metric existence to the tick, needs careful
  reset on device set changes, and spreads metric logic across the plugin.
- *Trade-off:* temperature in metrics is up to 30s stale (see Open Questions).

### 2. New `internal/metrics` package; one-way layering `metrics → plugin`
`internal/metrics` defines the `Collector` and imports `internal/plugin` to read
snapshots. `plugin` does **not** import `metrics`. Layering stays acyclic:
`metrics → plugin → {device, cdi}`. `plugin` gains small read-only accessors
(`ResourceClass()`, a `Snapshot()` of cached per-device stats, `Allocations()`).

### 3. Shared collector over all plugins via a thread-safe provider
The `Collector` holds a provider `func() []*plugin.Plugin` rather than a fixed
slice, because the plugin set is replaced on kubelet restart. `main` supplies a
provider that returns the **current** plugins under the existing mutex, so
metrics follow re-registration automatically. The collector is registered once
into a dedicated `prometheus.Registry`.

### 4. Dedicated metrics HTTP server started in `main`
`main` starts one `net/http` server on `TT_METRICS_PORT` (default `9102`) serving
`/metrics` via `promhttp`, shut down on `ctx` cancellation like everything else.
Enabled by default; Helm `metrics.enabled=false` omits the port/flag.

### 5. Allocation counter as an atomic in `Plugin`
`Allocate` increments an `atomic.Uint64` per plugin; `Allocations()` reads it.
Simpler and lock-free versus reusing `mu`.

## Risks / Trade-offs

- [Temperature staleness ≤30s] → acceptable for thermal telemetry; if finer
  resolution is needed, temperature is a pure sysfs read and can be sampled on
  scrape later without touching heartbeat state.
- [New dependency `client_golang`] → widely used, well-maintained; pulls a few
  transitive deps. Vendored via go modules; CI covers build.
- [Extra listening port on a non-privileged pod] → 9102 is unprivileged
  (>1024); no host access added.
- [Concurrent read of cached snapshot vs tick write] → guard the cached snapshot
  with the existing `mu` (same pattern as `lastHeartbeats`).

## Open Questions

- Sample temperature fresh on scrape (pure read) instead of from the cached tick,
  for lower staleness? Default: cached, for consistency with kubelet's view.
- Also expose `tt_device_heartbeat` (last counter value) as a gauge? Deferred
  unless operators want it — health already encodes stall.
