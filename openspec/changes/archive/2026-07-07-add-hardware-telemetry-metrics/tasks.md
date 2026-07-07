## 1. Device sysfs readers

- [x] 1.1 Add readers in `internal/device` for the new sysfs values: `power1_input`/`power1_max` and `in0_input`/`curr1_input` (hwmon), `tt_aiclk`/`tt_arcclk`/`tt_axiclk`, `aer_dev_correctable`, and `current_link_speed`/`current_link_width`.
- [x] 1.2 Add parse helpers: PCIe AER `TOTAL_ERR_COR` line extraction, and `"8.0 GT/s"` → float GT/s. Each returns an error so callers can omit on failure.
- [x] 1.3 Table-driven unit tests for the parse helpers (valid, malformed, absent) using fake-sysfs temp dirs.

## 2. Plugin snapshot

- [x] 2.1 Extend `DeviceSnapshot` with power/voltage/current/clock raw fields (+ `Has*` flags), `PcieCorrErrors`, `PcieLinkGTps`, `PcieLinkWidth`, and identity strings (`CardType`, `Serial`, `AsicID`, `FwBundle`).
- [x] 2.2 Populate the new fields in `assess()` under `mu`, reusing the temperature read path; leave `Has*` false when a sensor is absent.
- [x] 2.3 Unit test: after a health tick, `Snapshot()` reflects the new hardware fields (fake-sysfs), and absent sensors leave `Has*` false.

## 3. Metrics collector

- [x] 3.1 Add descriptors and emit in `Collect()`: `tt_device_power_watts`, `tt_device_power_max_watts`, `tt_device_voltage_volts`, `tt_device_current_amps`, `tt_device_clock_mhz{clock}`, `tt_device_pcie_correctable_errors_total`, `tt_device_pcie_link_speed_gtps`, `tt_device_pcie_link_width`. Convert raw sysfs units to base units at emit time; omit series when the corresponding `Has*` is false.
- [x] 3.2 Add `tt_device_info{card_type,serial,asic_id,fw_bundle}` and `tt_build_info{version,commit}` as constant `1` info-metrics.
- [x] 3.3 Extend the collector test (`CollectAndCompare`) with the new series, including an "absent sensor → no series" case and the info metrics.

## 4. Build wiring

- [x] 4.1 Add `version`/`commit` variables in `cmd/tt-device-plugin` set via `-ldflags`, and feed them into `tt_build_info`.

## 5. Verify

- [x] 5.1 `hack/check.sh` green (`go build`/`vet`/`test -race`, `golangci-lint`, `helm lint`).
- [x] 5.2 Deploy via `hack/dev-deploy.sh`, `curl` `/metrics`, and confirm real values on the n150: power ≈ 15 W, `tt_device_clock_mhz{clock="ai"}` ≈ 500, `tt_device_pcie_correctable_errors_total` = 0, and `tt_device_info` present.
