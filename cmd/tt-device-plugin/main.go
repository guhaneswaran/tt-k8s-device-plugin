package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/fsnotify/fsnotify"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/guhaneswaran/tt-k8s-device-plugin/internal/cdi"
	"github.com/guhaneswaran/tt-k8s-device-plugin/internal/device"
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
