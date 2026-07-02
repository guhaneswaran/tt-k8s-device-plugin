package cdi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/guhaneswaran/tt-k8s-device-plugin/internal/device"
)

func TestKindAndQualifiedName(t *testing.T) {
	if got := Kind("n150"); got != "tenstorrent.com/n150" {
		t.Errorf("Kind = %q, want tenstorrent.com/n150", got)
	}
	if got := QualifiedName("n150", "0"); got != "tenstorrent.com/n150=0" {
		t.Errorf("QualifiedName = %q, want tenstorrent.com/n150=0", got)
	}
}

func TestWriteSpec(t *testing.T) {
	dir := t.TempDir()
	devs := []device.Device{
		{ID: "0", DevPath: "/dev/tenstorrent/0"},
		{ID: "1", DevPath: "/dev/tenstorrent/1"},
	}

	// hugepagesPath points nowhere, so no hugepages mount should be added.
	if err := WriteSpec(dir, "n150", devs, filepath.Join(dir, "no-hugepages")); err != nil {
		t.Fatalf("WriteSpec: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, SpecFileName("n150")))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}

	var s spec
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}

	if s.CDIVersion != specVersion {
		t.Errorf("cdiVersion = %q, want %q", s.CDIVersion, specVersion)
	}
	if s.Kind != "tenstorrent.com/n150" {
		t.Errorf("kind = %q, want tenstorrent.com/n150", s.Kind)
	}
	if len(s.Devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(s.Devices))
	}
	if s.Devices[0].Name != "0" || s.Devices[0].ContainerEdits.DeviceNodes[0].Path != "/dev/tenstorrent/0" {
		t.Errorf("device 0 wrong: %+v", s.Devices[0])
	}

	// Common edits must carry the read-only /sys mount and nothing more here.
	if s.ContainerEdits == nil || len(s.ContainerEdits.Mounts) != 1 {
		t.Fatalf("expected exactly one common mount (/sys), got %+v", s.ContainerEdits)
	}
	m := s.ContainerEdits.Mounts[0]
	if m.HostPath != "/sys" || !hasOption(m.Options, "rbind") || !hasOption(m.Options, "ro") {
		t.Errorf("expected read-only rbind /sys mount, got %+v", m)
	}
}

func hasOption(opts []string, want string) bool {
	for _, o := range opts {
		if o == want {
			return true
		}
	}
	return false
}

func TestWriteSpecWithHugepages(t *testing.T) {
	dir := t.TempDir()
	hugepages := filepath.Join(dir, "hugepages")
	if err := os.Mkdir(hugepages, 0o755); err != nil {
		t.Fatal(err)
	}

	devs := []device.Device{{ID: "0", DevPath: "/dev/tenstorrent/0"}}
	if err := WriteSpec(dir, "n150", devs, hugepages); err != nil {
		t.Fatalf("WriteSpec: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, SpecFileName("n150")))
	if err != nil {
		t.Fatal(err)
	}
	var s spec
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}

	// /sys plus hugepages.
	if s.ContainerEdits == nil || len(s.ContainerEdits.Mounts) != 2 {
		t.Fatalf("expected /sys and hugepages mounts, got %+v", s.ContainerEdits)
	}
}

func TestRemoveSpec(t *testing.T) {
	dir := t.TempDir()
	devs := []device.Device{{ID: "0", DevPath: "/dev/tenstorrent/0"}}
	if err := WriteSpec(dir, "n150", devs, ""); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSpec(dir, "n150"); err != nil {
		t.Fatalf("RemoveSpec: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, SpecFileName("n150"))); !os.IsNotExist(err) {
		t.Error("spec file should be gone after RemoveSpec")
	}
	// Removing a non-existent spec is not an error.
	if err := RemoveSpec(dir, "n150"); err != nil {
		t.Errorf("RemoveSpec on missing file should be nil, got %v", err)
	}
}
