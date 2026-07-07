## Why

The `device-metrics` capability today exposes only health, temperature and
allocation/inventory counters. The Tenstorrent sysfs interface already reports
power, voltage, current, clock frequencies, PCIe link state and error counters,
and stable card identity — all readable with the same file-based pattern the
plugin uses for temperature. Surfacing them moves us toward the telemetry
breadth that NVIDIA (DCGM), AMD and Intel exporters provide, and closes the
open question in RESEARCH.md about TT's PCIe/error counters — without adding any
new dependency or privilege.

## What Changes

- Add hardware-telemetry series to the existing `/metrics` endpoint, sourced from
  sysfs and cached in the per-device snapshot alongside the current temperature
  read (same `mu`-guarded `assess()` path):
  - Power: `tt_device_power_watts`, `tt_device_power_max_watts`
  - Electrical: `tt_device_voltage_volts`, `tt_device_current_amps`
  - Clocks: `tt_device_clock_mhz{clock="ai|arc|axi"}`
  - PCIe: `tt_device_pcie_correctable_errors_total` (counter),
    `tt_device_pcie_link_speed_gtps`, `tt_device_pcie_link_width`
  - Identity: `tt_device_info{card_type,serial,asic_id,fw_bundle}` (gauge = 1)
  - Build: `tt_build_info{version,commit}` (gauge = 1)
- A missing sensor omits its series (same rule as the existing `HasTemp`
  handling), so the exporter degrades cleanly across card types.
- No new environment variables, ports, privileges, or dependencies.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `device-metrics`: add requirements for per-device hardware-telemetry series
  (power, electrical, clocks, PCIe link/errors), a device identity metric, and a
  build-info metric, all served on the existing endpoint and side-effect free.

## Non-goals

- **Deep telemetry that needs the TT telemetry library (luwen/tt-smi), not
  sysfs** — compute/Tensix utilization, DRAM used/free/bandwidth, memory
  temperature, energy accumulation, throttle reasons, and Ethernet-fabric
  metrics. These belong to a future standalone `tt-metrics-exporter` (P1),
  mirroring how NVIDIA/AMD split deep telemetry out of the device plugin.
- No new endpoint, port, env var, privilege, or dependency.
- No change to health logic, Allocate behavior, or the device-plugin gRPC API.

## Impact

- **Code**: `internal/device` (sysfs readers + parse helpers), `internal/plugin`
  (extend `DeviceSnapshot`, populate in `assess()`, accessors),
  `internal/metrics` (new descriptors + `Collect()` + info metrics),
  `cmd/tt-device-plugin` (build-info ldflags).
- **API**: additive only — new metric series; no change to the device-plugin
  gRPC surface, Allocate behavior, or Helm values.
- **Dependencies**: none (reuses `prometheus/client_golang`).
