## ADDED Requirements

### Requirement: Per-device hardware telemetry metrics
The plugin SHALL expose per-device power, electrical and clock telemetry read
from sysfs on the existing `/metrics` endpoint. Values SHALL be reported in base
units: power and its limit in watts, voltage in volts, current in amperes, and
clock frequencies in megahertz. Clock series SHALL carry a `clock` label
distinguishing the `ai`, `arc` and `axi` domains. When a sensor is unreadable
for a device, its series SHALL be omitted rather than reported as zero.

#### Scenario: Power, voltage and current are exposed
- **WHEN** a device exposes power, voltage and current sysfs sensors
- **THEN** `tt_device_power_watts`, `tt_device_power_max_watts`,
  `tt_device_voltage_volts` and `tt_device_current_amps` are emitted for that
  device in base units

#### Scenario: Clock frequencies are labeled by domain
- **WHEN** a device exposes AI, ARC and AXI clock sysfs values
- **THEN** `tt_device_clock_mhz` is emitted once per domain with a `clock` label
  of `ai`, `arc` or `axi`

#### Scenario: Missing sensor omits its series
- **WHEN** a sensor file is absent or unreadable for a device
- **THEN** the corresponding series is not emitted for that device (no zero value)

### Requirement: PCIe link and error metrics
The plugin SHALL expose per-device PCIe health from sysfs: a monotonically
increasing count of correctable errors as a counter, and the current link speed
(in GT/s) and link width as gauges.

#### Scenario: PCIe link state is exposed
- **WHEN** a device reports current PCIe link speed and width
- **THEN** `tt_device_pcie_link_speed_gtps` and `tt_device_pcie_link_width` are
  emitted for that device

#### Scenario: Correctable errors are a counter
- **WHEN** the device's PCIe AER correctable error total is readable
- **THEN** `tt_device_pcie_correctable_errors_total` is emitted as a counter
  reflecting the cumulative error count

### Requirement: Device identity metric
The plugin SHALL expose a per-device identity metric with a constant value of 1,
carrying the card type, serial, ASIC id and firmware bundle version as labels,
so telemetry can be joined to specific hardware.

#### Scenario: Identity is exposed as an info metric
- **WHEN** a device's identity attributes are readable from sysfs
- **THEN** `tt_device_info` is emitted with value 1 and labels `card_type`,
  `serial`, `asic_id` and `fw_bundle`

### Requirement: Build information metric
The plugin SHALL expose a single build-information metric with a constant value
of 1, carrying the plugin version and commit as labels.

#### Scenario: Build info is exposed
- **WHEN** the plugin is scraped
- **THEN** `tt_build_info` is emitted once with value 1 and labels `version` and
  `commit`
