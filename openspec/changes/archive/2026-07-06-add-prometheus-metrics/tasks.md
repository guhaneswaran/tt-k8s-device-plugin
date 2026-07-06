## 1. Plugin snapshot & counters

- [x] 1.1 Add an `atomic.Uint64` allocation counter to `Plugin`; increment it in `Allocate` by the number of containers served.
- [x] 1.2 Cache the last-computed health (and last temperature / max-temperature, in millidegrees) per device when `buildDeviceList`/`checkHealth` runs, guarded by `mu`.
- [x] 1.3 Add read-only accessors on `Plugin`: `ResourceClass() string`, `Allocations() uint64`, and `Snapshot() []DeviceSnapshot` (id, healthy, temp, maxTemp, hasTemp) read under `mu`.

## 2. metrics package

- [x] 2.1 Add the `github.com/prometheus/client_golang` dependency (`go get`), tidy `go.mod`/`go.sum`.
- [x] 2.2 Create `internal/metrics` with a `Collector` implementing `prometheus.Collector` (`Describe`/`Collect`), holding a provider `func() []*plugin.Plugin` (so it follows kubelet-restart re-creation).
- [x] 2.3 In `Collect()`, emit `tt_device_health`, `tt_device_temperature_celsius`, `tt_device_temperature_max_celsius` (per device), and `tt_devices_total`, `tt_allocations_total` (per class) from each plugin's `Snapshot()`/`Allocations()`.
- [x] 2.4 Add a `Handler()` helper that registers the collector plus default Go/process collectors into a `prometheus.Registry` and returns an `http.Handler` for `/metrics`.

## 3. main wiring

- [x] 3.1 Parse `TT_METRICS_PORT` (default `9102`); treat unset/invalid as default, and support a disabled mode.
- [x] 3.2 Start one metrics HTTP server in `main` with a thread-safe provider returning the current plugins (under the existing mutex); shut it down on `ctx` cancellation.

## 4. Deployment (Helm)

- [x] 4.1 Add `metrics.enabled` (default true) and `metrics.port` (default 9102) to `values.yaml`.
- [x] 4.2 Template the `TT_METRICS_PORT` env var and a `containerPort` in the DaemonSet when `metrics.enabled`; omit both when disabled.

## 5. Tests

- [x] 5.1 Table-driven unit test for the collector using a fake source (healthy, unhealthy, no-hwmon, multi-device, allocation counts) via `prometheus/testutil` (`CollectAndCompare`).
- [x] 5.2 Unit test that `Snapshot()` and `Allocations()` reflect health ticks and `Allocate` calls.
- [x] 5.3 `helm template` test: metrics env + containerPort render when enabled and are absent when disabled.

## 6. Verify

- [x] 6.1 `go build ./...`, `go vet ./...`, `go test -race ./...`, `golangci-lint run`, `helm lint` — all green.
- [x] 6.2 Deploy via `hack/dev-deploy.sh`, `curl` the `/metrics` endpoint, confirm `tt_device_*` series are present and `tt_device_health` flips to 0 under the strict temperature-limit trick, then recovers.
