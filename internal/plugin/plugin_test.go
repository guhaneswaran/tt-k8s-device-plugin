package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/guhaneswaran/tt-k8s-device-plugin/internal/device"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func TestCheckHealthTemp(t *testing.T) {
	hwmon := filepath.Join(t.TempDir(), "hwmon0")
	if err := os.MkdirAll(hwmon, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hwmon, "temp1_input"), []byte("45000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &Plugin{lastHeartbeats: make(map[string]string)}
	dev := device.Device{ID: "0", HwmonDir: hwmon}

	if got := p.checkHealth(dev); got != pluginapi.Healthy {
		t.Errorf("expected healthy, got %s", got)
	}

	if err := os.Remove(filepath.Join(hwmon, "temp1_input")); err != nil {
		t.Fatal(err)
	}
	if got := p.checkHealth(dev); got != pluginapi.Unhealthy {
		t.Errorf("expected unhealthy after sensor removal, got %s", got)
	}
}

func TestCheckHealthHeartbeat(t *testing.T) {
	sysfs := t.TempDir()
	if err := os.WriteFile(filepath.Join(sysfs, "tt_heartbeat"), []byte("100\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &Plugin{lastHeartbeats: make(map[string]string)}
	dev := device.Device{ID: "0", SysfsDir: sysfs}

	if got := p.checkHealth(dev); got != pluginapi.Healthy {
		t.Errorf("first check: expected healthy, got %s", got)
	}
	// Same value on second read → ARC frozen.
	if got := p.checkHealth(dev); got != pluginapi.Unhealthy {
		t.Errorf("stalled heartbeat: expected unhealthy, got %s", got)
	}

	if err := os.WriteFile(filepath.Join(sysfs, "tt_heartbeat"), []byte("200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := p.checkHealth(dev); got != pluginapi.Healthy {
		t.Errorf("advanced heartbeat: expected healthy, got %s", got)
	}
}

func TestAllocateHugepagesConditional(t *testing.T) {
	// Use New() so byID is populated; the socket path doesn't matter for Allocate.
	p := New("n150", []device.Device{
		{ID: "0", DevPath: "/dev/tenstorrent/0"},
	})

	req := &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{
			{DevicesIds: []string{"0"}},
		},
	}

	resp, err := p.Allocate(context.Background(), req)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	cr := resp.ContainerResponses[0]

	hasSys := false
	for _, m := range cr.Mounts {
		if m.HostPath == "/sys" {
			hasSys = true
		}
	}
	if !hasSys {
		t.Error("expected /sys mount")
	}

	if cr.Envs["TT_VISIBLE_DEVICES"] != "0" {
		t.Errorf("expected TT_VISIBLE_DEVICES=0, got %s", cr.Envs["TT_VISIBLE_DEVICES"])
	}

	if len(cr.Devices) != 1 || cr.Devices[0].HostPath != "/dev/tenstorrent/0" {
		t.Error("expected device spec for /dev/tenstorrent/0")
	}
}

func TestAllocateUnknownDevice(t *testing.T) {
	p := New("n150", []device.Device{
		{ID: "0", DevPath: "/dev/tenstorrent/0"},
	})

	req := &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{
			{DevicesIds: []string{"99"}},
		},
	}

	_, err := p.Allocate(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for unknown device ID, got nil")
	}
}
