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

	health  *prometheus.Desc
	temp    *prometheus.Desc
	tempMax *prometheus.Desc
	devices *prometheus.Desc
	allocs  *prometheus.Desc
}

// NewCollector builds a Collector. The provider is called on every scrape so the
// metrics follow plugin re-creation (e.g. after a kubelet restart).
func NewCollector(sources func() []Source) *Collector {
	perDevice := []string{"class", "device_id"}
	perClass := []string{"class"}
	return &Collector{
		sources: sources,
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
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.health
	ch <- c.temp
	ch <- c.tempMax
	ch <- c.devices
	ch <- c.allocs
}

// Collect implements prometheus.Collector. It reads each source's cached
// snapshot and emits one metric sample per (class, device).
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
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
		}
	}
}

// Handler builds an http.Handler that serves the tt_* metrics plus default
// Go/process metrics on its own registry.
func Handler(sources func() []Source) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		NewCollector(sources),
	)
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
