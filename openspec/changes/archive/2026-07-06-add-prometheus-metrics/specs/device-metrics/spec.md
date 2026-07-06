## ADDED Requirements

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
