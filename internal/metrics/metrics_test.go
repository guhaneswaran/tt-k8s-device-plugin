package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/guhaneswaran/tt-k8s-device-plugin/internal/plugin"
)

// fakeSource is a stand-in for *plugin.Plugin so we can feed exact device states.
type fakeSource struct {
	class  string
	allocs uint64
	snaps  []plugin.DeviceSnapshot
}

func (f fakeSource) ResourceClass() string             { return f.class }
func (f fakeSource) Allocations() uint64               { return f.allocs }
func (f fakeSource) Snapshot() []plugin.DeviceSnapshot { return f.snaps }

func TestCollector(t *testing.T) {
	src := fakeSource{
		class:  "n150",
		allocs: 3,
		snaps: []plugin.DeviceSnapshot{
			{ID: "0", Healthy: true, HasTemp: true, TempMilliC: 48000, MaxMilliC: 75000},
			{ID: "1", Healthy: false}, // unhealthy, no temperature sensor
		},
	}
	c := NewCollector(func() []Source { return []Source{src} }, "v1", "abc")

	// Device 1 has HasTemp=false, so it must emit NO temperature series.
	expected := `
# HELP tt_allocations_total Cumulative number of containers allocated for the class.
# TYPE tt_allocations_total counter
tt_allocations_total{class="n150"} 3
# HELP tt_device_health Device health: 1 if healthy, 0 if unhealthy.
# TYPE tt_device_health gauge
tt_device_health{class="n150",device_id="0"} 1
tt_device_health{class="n150",device_id="1"} 0
# HELP tt_device_temperature_celsius Current device temperature in degrees Celsius.
# TYPE tt_device_temperature_celsius gauge
tt_device_temperature_celsius{class="n150",device_id="0"} 48
# HELP tt_device_temperature_max_celsius Device temperature limit in degrees Celsius.
# TYPE tt_device_temperature_max_celsius gauge
tt_device_temperature_max_celsius{class="n150",device_id="0"} 75
# HELP tt_devices_total Number of discovered devices in the class.
# TYPE tt_devices_total gauge
tt_devices_total{class="n150"} 2
`
	names := []string{"tt_allocations_total", "tt_device_health", "tt_device_temperature_celsius", "tt_device_temperature_max_celsius", "tt_devices_total"}
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), names...); err != nil {
		t.Errorf("collector output mismatch:\n%v", err)
	}
}

func TestCollectorTelemetry(t *testing.T) {
	src := fakeSource{
		class: "n150",
		snaps: []plugin.DeviceSnapshot{
			{
				ID: "0", Healthy: true,
				HasPower: true, PowerMicroW: 15000000, PowerMaxMicroW: 100000000,
				HasVoltage: true, VoltageMilliV: 795,
				HasCurrent: true, CurrentMilliA: 18000,
				HasAiClk: true, AiClkMHz: 500,
				// ArcClk/AxiClk absent → must not be emitted.
				HasPcieErrors: true, PcieCorrErrors: 0,
				HasPcieLink: true, PcieLinkGTps: 8, PcieLinkWidth: 16,
				CardType: "n150", Serial: "S1", AsicID: "A1", FwBundle: "19.6.0.0",
			},
		},
	}
	c := NewCollector(func() []Source { return []Source{src} }, "v2", "def")

	expected := `
# HELP tt_build_info Plugin build information; constant 1 with version/commit labels.
# TYPE tt_build_info gauge
tt_build_info{commit="def",version="v2"} 1
# HELP tt_device_clock_mhz Device clock frequency in MHz, by clock domain.
# TYPE tt_device_clock_mhz gauge
tt_device_clock_mhz{class="n150",clock="ai",device_id="0"} 500
# HELP tt_device_current_amps Current device current draw in amperes.
# TYPE tt_device_current_amps gauge
tt_device_current_amps{class="n150",device_id="0"} 18
# HELP tt_device_info Device identity; constant 1 with identity labels.
# TYPE tt_device_info gauge
tt_device_info{asic_id="A1",card_type="n150",class="n150",device_id="0",fw_bundle="19.6.0.0",serial="S1"} 1
# HELP tt_device_pcie_correctable_errors_total Cumulative PCIe correctable errors (AER).
# TYPE tt_device_pcie_correctable_errors_total counter
tt_device_pcie_correctable_errors_total{class="n150",device_id="0"} 0
# HELP tt_device_pcie_link_speed_gtps Current PCIe link speed in GT/s.
# TYPE tt_device_pcie_link_speed_gtps gauge
tt_device_pcie_link_speed_gtps{class="n150",device_id="0"} 8
# HELP tt_device_pcie_link_width Current PCIe link width in lanes.
# TYPE tt_device_pcie_link_width gauge
tt_device_pcie_link_width{class="n150",device_id="0"} 16
# HELP tt_device_power_watts Current device power draw in watts.
# TYPE tt_device_power_watts gauge
tt_device_power_watts{class="n150",device_id="0"} 15
# HELP tt_device_power_max_watts Device power limit in watts.
# TYPE tt_device_power_max_watts gauge
tt_device_power_max_watts{class="n150",device_id="0"} 100
# HELP tt_device_voltage_volts Current device core voltage in volts.
# TYPE tt_device_voltage_volts gauge
tt_device_voltage_volts{class="n150",device_id="0"} 0.795
`
	names := []string{
		"tt_build_info", "tt_device_clock_mhz", "tt_device_current_amps", "tt_device_info",
		"tt_device_pcie_correctable_errors_total", "tt_device_pcie_link_speed_gtps",
		"tt_device_pcie_link_width", "tt_device_power_watts", "tt_device_power_max_watts",
		"tt_device_voltage_volts",
	}
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), names...); err != nil {
		t.Errorf("telemetry output mismatch:\n%v", err)
	}
}

func TestCollectorMultipleClasses(t *testing.T) {
	sources := []Source{
		fakeSource{class: "n150", allocs: 1, snaps: []plugin.DeviceSnapshot{{ID: "0", Healthy: true}}},
		fakeSource{class: "blackhole", allocs: 0, snaps: []plugin.DeviceSnapshot{
			{ID: "0", Healthy: true}, {ID: "1", Healthy: true},
		}},
	}
	c := NewCollector(func() []Source { return sources }, "v1", "abc")

	// Only check the per-class device counts across both classes.
	expected := `
# HELP tt_devices_total Number of discovered devices in the class.
# TYPE tt_devices_total gauge
tt_devices_total{class="n150"} 1
tt_devices_total{class="blackhole"} 2
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "tt_devices_total"); err != nil {
		t.Errorf("device count mismatch:\n%v", err)
	}
}
