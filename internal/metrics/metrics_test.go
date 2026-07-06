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
	c := NewCollector(func() []Source { return []Source{src} })

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
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Errorf("collector output mismatch:\n%v", err)
	}
}

func TestCollectorMultipleClasses(t *testing.T) {
	sources := []Source{
		fakeSource{class: "n150", allocs: 1, snaps: []plugin.DeviceSnapshot{{ID: "0", Healthy: true}}},
		fakeSource{class: "blackhole", allocs: 0, snaps: []plugin.DeviceSnapshot{
			{ID: "0", Healthy: true}, {ID: "1", Healthy: true},
		}},
	}
	c := NewCollector(func() []Source { return sources })

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
