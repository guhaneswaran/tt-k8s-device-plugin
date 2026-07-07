// Package metrics exposes Tenstorrent device state as Prometheus metrics. It
// implements a custom prometheus.Collector that reads each plugin's cached
// snapshot on scrape, so metrics reflect the health the kubelet last saw without
// re-running the (stateful) health checks.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/guhaneswaran/tt-k8s-device-plugin/internal/plugin"
)

// Source is the read-only view of a plugin the collector needs. *plugin.Plugin
// satisfies it; tests can supply a fake.
type Source interface {
	ResourceClass() string
	Allocations() uint64
	Snapshot() []plugin.DeviceSnapshot
}

// Collector turns a set of Sources into Prometheus metrics on each scrape.
type Collector struct {
	sources func() []Source
	version string
	commit  string

	health  *prometheus.Desc
	temp    *prometheus.Desc
	tempMax *prometheus.Desc
	devices *prometheus.Desc
	allocs  *prometheus.Desc

	power         *prometheus.Desc
	powerMax      *prometheus.Desc
	voltage       *prometheus.Desc
	current       *prometheus.Desc
	clock         *prometheus.Desc
	pcieErrors    *prometheus.Desc
	pcieLinkSpeed *prometheus.Desc
	pcieLinkWidth *prometheus.Desc
	info          *prometheus.Desc
	buildInfo     *prometheus.Desc
}

// NewCollector builds a Collector. The provider is called on every scrape so the
// metrics follow plugin re-creation (e.g. after a kubelet restart). version and
// commit populate the tt_build_info metric.
func NewCollector(sources func() []Source, version, commit string) *Collector {
	perDevice := []string{"class", "device_id"}
	perClass := []string{"class"}
	return &Collector{
		sources: sources,
		version: version,
		commit:  commit,
		health: prometheus.NewDesc(
			"tt_device_health",
			"Device health: 1 if healthy, 0 if unhealthy.",
			perDevice, nil),
		temp: prometheus.NewDesc(
			"tt_device_temperature_celsius",
			"Current device temperature in degrees Celsius.",
			perDevice, nil),
		tempMax: prometheus.NewDesc(
			"tt_device_temperature_max_celsius",
			"Device temperature limit in degrees Celsius.",
			perDevice, nil),
		devices: prometheus.NewDesc(
			"tt_devices_total",
			"Number of discovered devices in the class.",
			perClass, nil),
		allocs: prometheus.NewDesc(
			"tt_allocations_total",
			"Cumulative number of containers allocated for the class.",
			perClass, nil),
		power: prometheus.NewDesc(
			"tt_device_power_watts",
			"Current device power draw in watts.",
			perDevice, nil),
		powerMax: prometheus.NewDesc(
			"tt_device_power_max_watts",
			"Device power limit in watts.",
			perDevice, nil),
		voltage: prometheus.NewDesc(
			"tt_device_voltage_volts",
			"Current device core voltage in volts.",
			perDevice, nil),
		current: prometheus.NewDesc(
			"tt_device_current_amps",
			"Current device current draw in amperes.",
			perDevice, nil),
		clock: prometheus.NewDesc(
			"tt_device_clock_mhz",
			"Device clock frequency in MHz, by clock domain.",
			[]string{"class", "device_id", "clock"}, nil),
		pcieErrors: prometheus.NewDesc(
			"tt_device_pcie_correctable_errors_total",
			"Cumulative PCIe correctable errors (AER).",
			perDevice, nil),
		pcieLinkSpeed: prometheus.NewDesc(
			"tt_device_pcie_link_speed_gtps",
			"Current PCIe link speed in GT/s.",
			perDevice, nil),
		pcieLinkWidth: prometheus.NewDesc(
			"tt_device_pcie_link_width",
			"Current PCIe link width in lanes.",
			perDevice, nil),
		info: prometheus.NewDesc(
			"tt_device_info",
			"Device identity; constant 1 with identity labels.",
			[]string{"class", "device_id", "card_type", "serial", "asic_id", "fw_bundle"}, nil),
		buildInfo: prometheus.NewDesc(
			"tt_build_info",
			"Plugin build information; constant 1 with version/commit labels.",
			[]string{"version", "commit"}, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.health
	ch <- c.temp
	ch <- c.tempMax
	ch <- c.devices
	ch <- c.allocs
	ch <- c.power
	ch <- c.powerMax
	ch <- c.voltage
	ch <- c.current
	ch <- c.clock
	ch <- c.pcieErrors
	ch <- c.pcieLinkSpeed
	ch <- c.pcieLinkWidth
	ch <- c.info
	ch <- c.buildInfo
}

// Collect implements prometheus.Collector. It reads each source's cached
// snapshot and emits one metric sample per (class, device).
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(c.buildInfo, prometheus.GaugeValue, 1, c.version, c.commit)

	for _, s := range c.sources() {
		class := s.ResourceClass()
		snaps := s.Snapshot()

		ch <- prometheus.MustNewConstMetric(c.devices, prometheus.GaugeValue, float64(len(snaps)), class)
		ch <- prometheus.MustNewConstMetric(c.allocs, prometheus.CounterValue, float64(s.Allocations()), class)

		for _, d := range snaps {
			health := 0.0
			if d.Healthy {
				health = 1.0
			}
			ch <- prometheus.MustNewConstMetric(c.health, prometheus.GaugeValue, health, class, d.ID)

			if d.HasTemp {
				ch <- prometheus.MustNewConstMetric(c.temp, prometheus.GaugeValue, float64(d.TempMilliC)/1000, class, d.ID)
				if d.MaxMilliC > 0 {
					ch <- prometheus.MustNewConstMetric(c.tempMax, prometheus.GaugeValue, float64(d.MaxMilliC)/1000, class, d.ID)
				}
			}

			if d.HasPower {
				ch <- prometheus.MustNewConstMetric(c.power, prometheus.GaugeValue, float64(d.PowerMicroW)/1e6, class, d.ID)
				if d.PowerMaxMicroW > 0 {
					ch <- prometheus.MustNewConstMetric(c.powerMax, prometheus.GaugeValue, float64(d.PowerMaxMicroW)/1e6, class, d.ID)
				}
			}
			if d.HasVoltage {
				ch <- prometheus.MustNewConstMetric(c.voltage, prometheus.GaugeValue, float64(d.VoltageMilliV)/1000, class, d.ID)
			}
			if d.HasCurrent {
				ch <- prometheus.MustNewConstMetric(c.current, prometheus.GaugeValue, float64(d.CurrentMilliA)/1000, class, d.ID)
			}
			if d.HasAiClk {
				ch <- prometheus.MustNewConstMetric(c.clock, prometheus.GaugeValue, float64(d.AiClkMHz), class, d.ID, "ai")
			}
			if d.HasArcClk {
				ch <- prometheus.MustNewConstMetric(c.clock, prometheus.GaugeValue, float64(d.ArcClkMHz), class, d.ID, "arc")
			}
			if d.HasAxiClk {
				ch <- prometheus.MustNewConstMetric(c.clock, prometheus.GaugeValue, float64(d.AxiClkMHz), class, d.ID, "axi")
			}
			if d.HasPcieErrors {
				ch <- prometheus.MustNewConstMetric(c.pcieErrors, prometheus.CounterValue, float64(d.PcieCorrErrors), class, d.ID)
			}
			if d.HasPcieLink {
				ch <- prometheus.MustNewConstMetric(c.pcieLinkSpeed, prometheus.GaugeValue, d.PcieLinkGTps, class, d.ID)
				ch <- prometheus.MustNewConstMetric(c.pcieLinkWidth, prometheus.GaugeValue, float64(d.PcieLinkWidth), class, d.ID)
			}
			ch <- prometheus.MustNewConstMetric(c.info, prometheus.GaugeValue, 1, class, d.ID, d.CardType, d.Serial, d.AsicID, d.FwBundle)
		}
	}
}

// Handler builds an http.Handler that serves the tt_* metrics plus default
// Go/process metrics on its own registry. version and commit populate
// tt_build_info.
func Handler(sources func() []Source, version, commit string) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		NewCollector(sources, version, commit),
	)
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
