package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/guhaneswaran/tt-k8s-device-plugin/internal/cdi"
	"github.com/guhaneswaran/tt-k8s-device-plugin/internal/device"
	"github.com/guhaneswaran/tt-k8s-device-plugin/internal/metrics"
	"github.com/guhaneswaran/tt-k8s-device-plugin/internal/plugin"
)

var version = "dev"

func main() {
	klog.InitFlags(nil)
	klog.Infof("Tenstorrent device plugin %s", version)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	plugins, err := startPlugins(ctx)
	if err != nil {
		klog.Fatalf("Startup failed: %v", err)
	}

	var mu sync.Mutex

	if metricsEnabled() {
		// Provider reads the current plugin set under mu, so metrics follow
		// kubelet-restart re-creation. *plugin.Plugin satisfies metrics.Source.
		provider := func() []metrics.Source {
			mu.Lock()
			defer mu.Unlock()
			srcs := make([]metrics.Source, len(plugins))
			for i, p := range plugins {
				srcs[i] = p
			}
			return srcs
		}
		go serveMetrics(ctx, ":"+metricsPort(), provider)
	}

	go watchKubelet(ctx, func() {
		mu.Lock()
		defer mu.Unlock()

		for _, p := range plugins {
			p.Stop()
		}

		plugins, err = startPlugins(ctx)
		if err != nil {
			klog.Errorf("Failed to restart plugins after kubelet restart: %v", err)
			plugins = nil
		}
	})

	<-ctx.Done()
	klog.Info("Shutting down")

	mu.Lock()
	for _, p := range plugins {
		p.Stop()
	}
	mu.Unlock()
}

func startPlugins(ctx context.Context) ([]*plugin.Plugin, error) {
	grouped, err := device.Discover()
	if err != nil {
		return nil, fmt.Errorf("device discovery: %w", err)
	}
	if len(grouped) == 0 {
		return nil, fmt.Errorf("no Tenstorrent devices found")
	}

	if os.Getenv("TT_CDI_ENABLED") == "true" {
		for class, devs := range grouped {
			if err := cdi.WriteSpec(cdi.DefaultSpecDir, class, devs, plugin.HugepagesPath); err != nil {
				klog.Errorf("Failed to write CDI spec for %s: %v", class, err)
			} else {
				klog.Infof("Wrote CDI spec for %s (%d devices) to %s", class, len(devs), cdi.DefaultSpecDir)
			}
		}
	}

	plugins := make([]*plugin.Plugin, 0, len(grouped))
	for class, devs := range grouped {
		p := plugin.New(class, devs)
		plugins = append(plugins, p)

		go func(p *plugin.Plugin, class string) {
			if err := p.Run(ctx); err != nil {
				klog.Errorf("Plugin %s error: %v", class, err)
			}
		}(p, class)
	}
	return plugins, nil
}

// metricsEnabled reports whether the metrics server should run. Enabled unless
// TT_METRICS_ENABLED is explicitly "false".
func metricsEnabled() bool {
	return os.Getenv("TT_METRICS_ENABLED") != "false"
}

// metricsPort returns the metrics port (TT_METRICS_PORT, default 9102).
func metricsPort() string {
	if v := strings.TrimSpace(os.Getenv("TT_METRICS_PORT")); v != "" {
		return v
	}
	return "9102"
}

// serveMetrics runs the /metrics HTTP server until ctx is cancelled.
func serveMetrics(ctx context.Context, addr string, provider func() []metrics.Source) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler(provider))
	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	klog.Infof("Serving metrics on %s/metrics", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		klog.Errorf("Metrics server error: %v", err)
	}
}

func watchKubelet(ctx context.Context, restart func()) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		klog.Fatalf("Failed to create fsnotify watcher: %v", err)
	}
	defer func() { _ = watcher.Close() }()

	if err := watcher.Add(pluginapi.DevicePluginPath); err != nil {
		klog.Fatalf("Failed to watch %s: %v", pluginapi.DevicePluginPath, err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-watcher.Events:
			if event.Name == pluginapi.KubeletSocket && event.Has(fsnotify.Create) {
				klog.Info("Kubelet restarted, re-registering")
				restart()
			}
		case err := <-watcher.Errors:
			klog.Errorf("fsnotify error: %v", err)
		}
	}
}
