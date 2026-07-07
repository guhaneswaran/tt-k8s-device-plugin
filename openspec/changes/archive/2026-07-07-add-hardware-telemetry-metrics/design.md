## Context

The `device-metrics` capability already runs a `prometheus.Collector` fed by the
plugin's per-device `DeviceSnapshot` (health + temperature), refreshed under `mu`
during the 30s `assess()`/`buildDeviceList` cycle. On the real n150, sysfs
exposes more than temperature — power, voltage, current, three clock domains,
PCIe link state and AER correctable errors, and stable card identity — all as
plain files under the device's hwmon and `tenstorrent!N` directories. This change
adds those to the existing snapshot and collector; it does not introduce a new
component. Deep telemetry (utilization, DRAM, fabric) needs the TT telemetry
library and is deferred to a separate exporter.

## Goals / Non-Goals

**Goals:**
- Extend the existing `/metrics` with sysfs-backed hardware telemetry, reusing
  the temperature read path (same `mu`-guarded snapshot).
- Keep the exporter honest on heterogeneous hardware: omit any series whose
  sensor is absent.
- No new endpoint, dependency, or privilege.

**Non-Goals:**
- Compute/Tensix utilization, DRAM used/free/bandwidth, memory temperature,
  energy, throttle reasons, Ethernet-fabric metrics — these need luwen/tt-smi
  and belong to the future standalone `tt-metrics-exporter`.

## Decisions

- **Store raw sysfs units in the snapshot; convert in `Collect()`.** The
  snapshot keeps integers as sysfs reports them (microwatts, millivolts,
  milliamps, millidegrees), mirroring how temperature is already handled; the
  collector divides to base units at emit time. Keeps the read path allocation-
  free and unit conversion in one place. *Alternative:* convert at read time —
  rejected; it scatters float math through the plugin and diverges from the
  existing temp handling.
- **One `tt_device_clock_mhz` metric with a `clock` label**, not three separate
  metrics. Matches AMD's `GPU_CLOCK{clock=...}` shape and keeps the series set
  small. *Alternative:* `tt_device_aiclk_mhz` etc. — rejected as less scalable.
- **PCIe correctable errors as a counter** (`_total`), link speed/width as
  gauges. Errors are cumulative and monotonic (rate-able); link state is a
  current value. Consistent with the counter/gauge split already established.
- **Identity and build via info-metrics** (`tt_device_info`, `tt_build_info`,
  value = 1, data in labels). This is the standard Prometheus "info" pattern and
  keeps identity joinable to every other series without inflating cardinality of
  numeric metrics.
- **Omit-on-missing, per sensor.** Extends the existing `HasTemp` approach to
  every new field via `Has*` flags in the snapshot, so a card lacking a sensor
  (or a differing sysfs layout) degrades cleanly instead of emitting zeros.

## Risks / Trade-offs

- [sysfs field names/units differ across card generations] → only n150 is
  verified; the omit-on-missing rule and parse helpers with error returns keep
  unknown/absent fields from breaking the scrape. Multi-class validation is a
  separate roadmap item.
- [`aer_dev_correctable` format is multi-line] → parse the `TOTAL_ERR_COR` line
  specifically, with a unit-tested helper; on parse failure the series is omitted.
- [label cardinality from `tt_device_info`] → bounded (one per physical device),
  so no cardinality risk.
