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

func TestAllocationsCounter(t *testing.T) {
	p := New("n150", []device.Device{{ID: "0", DevPath: "/dev/tenstorrent/0"}})
	if got := p.Allocations(); got != 0 {
		t.Fatalf("initial allocations = %d, want 0", got)
	}

	req := &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{
			{DevicesIds: []string{"0"}},
			{DevicesIds: []string{"0"}},
		},
	}
	if _, err := p.Allocate(context.Background(), req); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if got := p.Allocations(); got != 2 {
		t.Errorf("allocations = %d, want 2 (two containers)", got)
	}
}

func TestSnapshotReflectsTelemetry(t *testing.T) {
	root := t.TempDir()
	hwmon := filepath.Join(root, "hwmon")
	pciDev := filepath.Join(root, "device")
	for _, d := range []string{hwmon, pciDev} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(dir, name, val string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(val), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(hwmon, "temp1_input", "47000\n")
	write(hwmon, "temp1_max", "75000\n")
	write(hwmon, "power1_input", "15000000\n")
	write(hwmon, "power1_max", "100000000\n")
	write(hwmon, "in0_input", "795\n")
	write(hwmon, "curr1_input", "18000\n")
	write(root, "tt_aiclk", "500\n")
	write(root, "tt_serial", "SERIAL123\n")
	write(root, "tt_asic_id", "ASIC123\n")
	write(root, "tt_fw_bundle_ver", "19.6.0.0\n")
	write(pciDev, "aer_dev_correctable", "RxErr 0\nTOTAL_ERR_COR 0\n")
	write(pciDev, "current_link_speed", "8.0 GT/s PCIe\n")
	write(pciDev, "current_link_width", "16\n")

	p := New("n150", []device.Device{{ID: "0", CardType: "n150", HwmonDir: hwmon, SysfsDir: root}})
	p.buildDeviceList()

	snaps := p.Snapshot()
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	s := snaps[0]
	if !s.HasPower || s.PowerMicroW != 15000000 || s.PowerMaxMicroW != 100000000 {
		t.Errorf("power wrong: %+v", s)
	}
	if !s.HasVoltage || s.VoltageMilliV != 795 {
		t.Errorf("voltage wrong: %+v", s)
	}
	if !s.HasCurrent || s.CurrentMilliA != 18000 {
		t.Errorf("current wrong: %+v", s)
	}
	if !s.HasAiClk || s.AiClkMHz != 500 {
		t.Errorf("aiclk wrong: %+v", s)
	}
	if s.HasArcClk {
		t.Errorf("arcclk should be absent (no sysfs file): %+v", s)
	}
	if !s.HasPcieErrors || s.PcieCorrErrors != 0 {
		t.Errorf("pcie errors wrong: %+v", s)
	}
	if !s.HasPcieLink || s.PcieLinkGTps != 8.0 || s.PcieLinkWidth != 16 {
		t.Errorf("pcie link wrong: %+v", s)
	}
	if s.CardType != "n150" || s.Serial != "SERIAL123" || s.AsicID != "ASIC123" || s.FwBundle != "19.6.0.0" {
		t.Errorf("identity wrong: %+v", s)
	}
}

func TestSnapshotReflectsHealth(t *testing.T) {
	hwmon := filepath.Join(t.TempDir(), "hwmon0")
	if err := os.MkdirAll(hwmon, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, val string) {
		if err := os.WriteFile(filepath.Join(hwmon, name), []byte(val), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("temp1_max", "75000\n")
	write("temp1_input", "90000\n") // over the limit

	p := New("n150", []device.Device{{ID: "0", HwmonDir: hwmon}})

	// Seeded snapshot before any health tick: one device, healthy by default.
	snaps := p.Snapshot()
	if len(snaps) != 1 || !snaps[0].Healthy {
		t.Fatalf("seeded snapshot = %+v, want 1 healthy device", snaps)
	}

	// Run a health tick; the over-temp device must read unhealthy, with temps.
	p.buildDeviceList()
	snaps = p.Snapshot()
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	s := snaps[0]
	if s.Healthy {
		t.Error("expected unhealthy after over-temp tick")
	}
	if !s.HasTemp || s.TempMilliC != 90000 || s.MaxMilliC != 75000 {
		t.Errorf("snapshot temps wrong: %+v", s)
	}
}
