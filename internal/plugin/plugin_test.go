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

func TestCheckHealthTempThreshold(t *testing.T) {
	hwmon := filepath.Join(t.TempDir(), "hwmon0")
	if err := os.MkdirAll(hwmon, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, val string) {
		if err := os.WriteFile(filepath.Join(hwmon, name), []byte(val), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("temp1_max", "75000\n") // 75 C hardware limit

	p := &Plugin{lastHeartbeats: make(map[string]string)}
	dev := device.Device{ID: "0", HwmonDir: hwmon}

	// Below the sysfs limit -> healthy.
	write("temp1_input", "48000\n")
	if got := p.checkHealth(dev); got != pluginapi.Healthy {
		t.Errorf("48C < 75C: expected healthy, got %s", got)
	}

	// At/above the sysfs limit -> unhealthy.
	write("temp1_input", "75000\n")
	if got := p.checkHealth(dev); got != pluginapi.Unhealthy {
		t.Errorf("75C >= 75C: expected unhealthy, got %s", got)
	}
}

func TestCheckHealthTempEnvOverride(t *testing.T) {
	hwmon := filepath.Join(t.TempDir(), "hwmon0")
	if err := os.MkdirAll(hwmon, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hwmon, "temp1_max"), []byte("75000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hwmon, "temp1_input"), []byte("72000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dev := device.Device{ID: "0", HwmonDir: hwmon}

	// 72C is under the sysfs limit (75C) — healthy without an override.
	p := &Plugin{lastHeartbeats: make(map[string]string)}
	if got := p.checkHealth(dev); got != pluginapi.Healthy {
		t.Errorf("72C < 75C sysfs: expected healthy, got %s", got)
	}

	// A stricter override of 70C makes the same 72C reading unhealthy.
	t.Setenv("TT_TEMP_MAX_C", "70")
	strict := &Plugin{lastHeartbeats: make(map[string]string), tempMaxMilliC: parseTempMaxEnv()}
	if got := strict.checkHealth(dev); got != pluginapi.Unhealthy {
		t.Errorf("72C >= 70C override: expected unhealthy, got %s", got)
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

func TestAllocateCDI(t *testing.T) {
	t.Setenv("TT_CDI_ENABLED", "true")
	p := New("n150", []device.Device{
		{ID: "0", DevPath: "/dev/tenstorrent/0"},
		{ID: "1", DevPath: "/dev/tenstorrent/1"},
	})

	req := &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{
			{DevicesIds: []string{"0", "1"}},
		},
	}

	resp, err := p.Allocate(context.Background(), req)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	cr := resp.ContainerResponses[0]

	// CDI mode: device nodes and mounts move to the CDI spec, not the response.
	if len(cr.Devices) != 0 {
		t.Errorf("expected no raw device specs in CDI mode, got %d", len(cr.Devices))
	}
	if len(cr.Mounts) != 0 {
		t.Errorf("expected no mounts in CDI mode, got %d", len(cr.Mounts))
	}

	if len(cr.CdiDevices) != 2 {
		t.Fatalf("expected 2 CDI devices, got %d", len(cr.CdiDevices))
	}
	if cr.CdiDevices[0].Name != "tenstorrent.com/n150=0" {
		t.Errorf("CDI device 0 name = %q, want tenstorrent.com/n150=0", cr.CdiDevices[0].Name)
	}
	if cr.CdiDevices[1].Name != "tenstorrent.com/n150=1" {
		t.Errorf("CDI device 1 name = %q, want tenstorrent.com/n150=1", cr.CdiDevices[1].Name)
	}

	// The joined visible-devices env is still set from the request.
	if cr.Envs["TT_VISIBLE_DEVICES"] != "0,1" {
		t.Errorf("expected TT_VISIBLE_DEVICES=0,1, got %s", cr.Envs["TT_VISIBLE_DEVICES"])
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
