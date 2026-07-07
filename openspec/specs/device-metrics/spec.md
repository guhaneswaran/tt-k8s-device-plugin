# device-metrics Specification

## Purpose
TBD - created by archiving change add-prometheus-metrics. Update Purpose after archive.
## Requirements
### Requirement: Metrics HTTP endpoint
The plugin SHALL expose Prometheus metrics over HTTP at the `/metrics` path in
the Prometheus text exposition format. The listening port SHALL default to `9102`
and be configurable via the `TT_METRICS_PORT` environment variable. The endpoint
SHALL be enabled by default and MAY be disabled via deployment configuration.

#### Scenario: Endpoint serves metrics
- **WHEN** a client performs an HTTP GET on `/metrics`
- **THEN** the response status is 200 and the body is valid Prometheus text format

#### Scenario: Port is configurable
- **WHEN** `TT_METRICS_PORT` is set to a valid port
- **THEN** the metrics server listens on that port instead of `9102`

#### Scenario: Metrics can be disabled
- **WHEN** metrics are disabled via configuration
- **THEN** no metrics server is started and no metrics port is exposed

### Requirement: Per-device health metric
The plugin SHALL expose a gauge `tt_device_health` labeled by `class` and
`device_id`, with value `1` when the device is healthy and `0` when unhealthy.
The value SHALL reflect the plugin's most recent health assessment (the same one
reported to the kubelet).

#### Scenario: Healthy device
- **WHEN** a device's last health assessment was healthy
- **THEN** `tt_device_health{class,device_id}` is `1`

#### Scenario: Unhealthy device
- **WHEN** a device's last health assessment was unhealthy
- **THEN** `tt_device_health{class,device_id}` is `0`

### Requirement: Per-device temperature metrics
When a device exposes a hwmon temperature sensor, the plugin SHALL expose gauges
`tt_device_temperature_celsius` (current temperature) and
`tt_device_temperature_max_celsius` (the hardware limit), both labeled by `class`
and `device_id`, in degrees Celsius.

#### Scenario: Temperature reported in Celsius
- **WHEN** a device reports 47875 millidegrees from hwmon
- **THEN** `tt_device_temperature_celsius{class,device_id}` is `47.875`

#### Scenario: Device without a temperature sensor
- **WHEN** a device has no readable hwmon sensor
- **THEN** no temperature metric series is emitted for that device

### Requirement: Per-class inventory metrics
The plugin SHALL expose a gauge `tt_devices_total` labeled by `class` giving the
number of discovered devices in that class, and a counter
`tt_allocations_total` labeled by `class` giving the cumulative number of
`Allocate` requests served for that class.

#### Scenario: Device count reflects discovery
- **WHEN** two devices of class `blackhole` were discovered
- **THEN** `tt_devices_total{class="blackhole"}` is `2`

#### Scenario: Allocation counter increments
- **WHEN** the plugin serves an `Allocate` request for a class
- **THEN** `tt_allocations_total{class}` increases by the number of containers allocated

### Requirement: Scraping is side-effect free
Serving a metrics scrape SHALL NOT alter device health state, in particular it
SHALL NOT interfere with heartbeat-stall detection or its 30-second cadence.
Metric values SHALL be read from the health state last computed for the kubelet,
not recomputed on scrape. The endpoint SHALL also expose standard Go/process
metrics from the client library.

#### Scenario: Scrape does not affect health computation
- **WHEN** `/metrics` is scraped between two `ListAndWatch` health ticks
- **THEN** heartbeat-stall detection is unaffected and no additional heartbeat read is performed

#### Scenario: Standard runtime metrics present
- **WHEN** `/metrics` is scraped
- **THEN** default Go runtime and process metrics are included alongside the `tt_*` metrics

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

