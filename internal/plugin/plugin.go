// Package plugin implements the Kubernetes Device Plugin gRPC API for
// Tenstorrent AI accelerators. One Plugin instance is created per resource
// class (e.g. "tenstorrent.com/n150") and runs in its own goroutine.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/guhaneswaran/tt-k8s-device-plugin/internal/device"
)

const (
	resourceDomain  = "tenstorrent.com"
	healthInterval  = 30 * time.Second
	hugepagesPath   = "/dev/hugepages-1G"
	stopGracePeriod = 5 * time.Second
	readyTimeout    = 5 * time.Second
	registerTimeout = 10 * time.Second
)

// Plugin implements pluginapi.DevicePluginServer for a single resource class.
type Plugin struct {
	pluginapi.UnimplementedDevicePluginServer

	resourceName string
	socketName   string
	socketPath   string

	// devices is the ordered slice used by ListAndWatch (stable order matters
	// so kubelet does not see spurious state changes between ticks).
	devices []device.Device
	// byID provides O(1) device lookup in Allocate.
	byID map[string]*device.Device

	mu             sync.Mutex
	grpcServer     *grpc.Server // guarded by mu during serve/Stop overlap
	lastHeartbeats map[string]string
	stop           chan struct{}
}

// New constructs a Plugin for the given resource class and device list.
// It copies the device slice so the caller can discard its own reference.
func New(resourceClass string, devices []device.Device) *Plugin {
	devs := make([]device.Device, len(devices))
	copy(devs, devices)

	byID := make(map[string]*device.Device, len(devs))
	for i := range devs {
		byID[devs[i].ID] = &devs[i]
	}

	socketName := "tenstorrent-" + resourceClass + ".sock"
	return &Plugin{
		resourceName:   resourceDomain + "/" + resourceClass,
		socketName:     socketName,
		socketPath:     filepath.Join(pluginapi.DevicePluginPath, socketName),
		devices:        devs,
		byID:           byID,
		stop:           make(chan struct{}),
		lastHeartbeats: make(map[string]string),
	}
}

// Run starts the plugin: removes any stale socket, starts the gRPC server,
// registers with kubelet, then blocks until ctx is cancelled or Stop is called.
func (p *Plugin) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if err := removeSocket(p.socketPath); err != nil {
		return err
	}
	if err := p.serve(); err != nil {
		return err
	}
	if err := p.waitReady(ctx); err != nil {
		return err
	}
	if err := p.register(ctx); err != nil {
		return err
	}

	klog.Infof("Serving %s (%d devices) on %s", p.resourceName, len(p.devices), p.socketName)

	select {
	case <-ctx.Done():
	case <-p.stop:
	}
	return nil
}

// Stop gracefully shuts down the plugin. It is safe to call more than once.
func (p *Plugin) Stop() {
	select {
	case <-p.stop:
		return // already stopped
	default:
		close(p.stop)
	}

	p.mu.Lock()
	srv := p.grpcServer
	p.mu.Unlock()

	if srv != nil {
		drained := make(chan struct{})
		go func() {
			srv.GracefulStop()
			close(drained)
		}()
		select {
		case <-drained:
		case <-time.After(stopGracePeriod):
			klog.Warningf("GracefulStop timed out for %s, forcing", p.resourceName)
			srv.Stop()
		}
	}

	_ = removeSocket(p.socketPath)
}

// serve starts the gRPC server on the plugin's unix socket.
func (p *Plugin) serve() error {
	lis, err := net.Listen("unix", p.socketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", p.socketPath, err)
	}

	srv := grpc.NewServer()
	pluginapi.RegisterDevicePluginServer(srv, p)

	p.mu.Lock()
	p.grpcServer = srv
	p.mu.Unlock()

	go func() {
		if err := srv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			klog.Warningf("gRPC server stopped for %s: %v", p.resourceName, err)
		}
	}()

	return nil
}

// waitReady polls the plugin's own unix socket with short dials until it
// accepts a connection, confirming the gRPC server is ready. The overall
// deadline is min(ctx deadline, readyTimeout).
func (p *Plugin) waitReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()

	for {
		conn, err := net.DialTimeout("unix", p.socketPath, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("socket %s not ready within %s: %w", p.socketPath, readyTimeout, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// register tells kubelet about this plugin via kubelet's registration socket.
func (p *Plugin) register(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, registerTimeout)
	defer cancel()

	conn, err := grpc.NewClient("unix://"+pluginapi.KubeletSocket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dial kubelet: %w", err)
	}
	defer func() { _ = conn.Close() }()

	_, err = pluginapi.NewRegistrationClient(conn).Register(ctx, &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     p.socketName,
		ResourceName: p.resourceName,
	})
	if err != nil {
		return fmt.Errorf("register %s: %w", p.resourceName, err)
	}
	return nil
}

// GetDevicePluginOptions implements DevicePluginServer.
func (p *Plugin) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{}, nil
}

// ListAndWatch sends the full device list immediately, then re-sends every
// healthInterval to report updated health state.
func (p *Plugin) ListAndWatch(_ *pluginapi.Empty, stream pluginapi.DevicePlugin_ListAndWatchServer) error {
	if err := stream.Send(&pluginapi.ListAndWatchResponse{Devices: p.buildDeviceList()}); err != nil {
		return err
	}

	ticker := time.NewTicker(healthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stop:
			return nil
		case <-ticker.C:
			if err := stream.Send(&pluginapi.ListAndWatchResponse{Devices: p.buildDeviceList()}); err != nil {
				return err
			}
		}
	}
}

// Allocate responds to a kubelet allocation request by returning the device
// nodes, mounts, and environment variables each container needs.
func (p *Plugin) Allocate(_ context.Context, req *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	responses := make([]*pluginapi.ContainerAllocateResponse, 0, len(req.ContainerRequests))

	for _, creq := range req.ContainerRequests {
		devSpecs := make([]*pluginapi.DeviceSpec, 0, len(creq.DevicesIds))
		ids := make([]string, 0, len(creq.DevicesIds))

		for _, id := range creq.DevicesIds {
			dev, ok := p.byID[id]
			if !ok {
				return nil, status.Errorf(codes.InvalidArgument, "unknown device ID: %s", id)
			}
			devSpecs = append(devSpecs, &pluginapi.DeviceSpec{
				HostPath:      dev.DevPath,
				ContainerPath: dev.DevPath,
				Permissions:   "rw",
			})
			ids = append(ids, id)
		}

		mounts := []*pluginapi.Mount{{
			HostPath:      "/sys",
			ContainerPath: "/sys",
			ReadOnly:      true,
		}}
		if _, err := os.Stat(hugepagesPath); err == nil {
			mounts = append(mounts, &pluginapi.Mount{
				HostPath:      hugepagesPath,
				ContainerPath: hugepagesPath,
			})
		}

		responses = append(responses, &pluginapi.ContainerAllocateResponse{
			Envs:    map[string]string{"TT_VISIBLE_DEVICES": strings.Join(ids, ",")},
			Devices: devSpecs,
			Mounts:  mounts,
		})
	}

	return &pluginapi.AllocateResponse{ContainerResponses: responses}, nil
}

// GetPreferredAllocation implements DevicePluginServer (stub — no topology hints yet).
func (p *Plugin) GetPreferredAllocation(context.Context, *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	return &pluginapi.PreferredAllocationResponse{}, nil
}

// PreStartContainer implements DevicePluginServer (no-op).
func (p *Plugin) PreStartContainer(context.Context, *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

func (p *Plugin) buildDeviceList() []*pluginapi.Device {
	list := make([]*pluginapi.Device, len(p.devices))
	for i, dev := range p.devices {
		d := &pluginapi.Device{
			ID:     dev.ID,
			Health: p.checkHealth(dev),
		}
		if dev.NumaNode >= 0 {
			d.Topology = &pluginapi.TopologyInfo{
				Nodes: []*pluginapi.NUMANode{{ID: dev.NumaNode}},
			}
		}
		list[i] = d
	}
	return list
}

// checkHealth reports whether dev is healthy. Two independent signals:
//  1. Temperature sensor readable — proxy for the driver being alive.
//  2. ARC heartbeat advancing — proxy for on-card firmware not being frozen.
func (p *Plugin) checkHealth(dev device.Device) string {
	if dev.HwmonDir != "" {
		if _, err := os.ReadFile(filepath.Join(dev.HwmonDir, "temp1_input")); err != nil {
			klog.Warningf("Device %s unhealthy (temp sensor unreadable): %v", dev.ID, err)
			return pluginapi.Unhealthy
		}
	}

	if dev.SysfsDir != "" {
		if hb, err := device.Heartbeat(dev); err == nil {
			p.mu.Lock()
			prev, hasPrev := p.lastHeartbeats[dev.ID]
			p.lastHeartbeats[dev.ID] = hb
			p.mu.Unlock()

			if hasPrev && prev == hb {
				klog.Warningf("Device %s unhealthy (heartbeat stalled at %s)", dev.ID, hb)
				return pluginapi.Unhealthy
			}
		}
	}

	return pluginapi.Healthy
}

func removeSocket(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove socket %s: %w", path, err)
	}
	return nil
}
