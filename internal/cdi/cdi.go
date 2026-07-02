// Package cdi generates Container Device Interface (CDI) specifications for
// Tenstorrent accelerators. When CDI is enabled the plugin references devices
// by qualified name (e.g. "tenstorrent.com/n150=0") in Allocate, and the
// container runtime injects the device nodes, mounts, and env from the spec
// files this package writes under /var/run/cdi.
package cdi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/guhaneswaran/tt-k8s-device-plugin/internal/device"
)

const (
	// specVersion is the CDI schema version we emit.
	specVersion = "0.6.0"
	// vendor is the CDI vendor prefix; matches the device plugin resource domain.
	vendor = "tenstorrent.com"
	// DefaultSpecDir is the standard directory for dynamically generated CDI specs.
	DefaultSpecDir = "/var/run/cdi"
)

// Kind returns the CDI kind for a resource class, e.g. "tenstorrent.com/n150".
// It is identical to the device plugin resource name for that class.
func Kind(class string) string {
	return vendor + "/" + class
}

// QualifiedName returns the fully-qualified CDI device name for a single
// device, e.g. "tenstorrent.com/n150=0".
func QualifiedName(class, id string) string {
	return Kind(class) + "=" + id
}

// spec mirrors the subset of the CDI JSON schema (v0.6.0) we emit.
type spec struct {
	CDIVersion     string          `json:"cdiVersion"`
	Kind           string          `json:"kind"`
	Devices        []specDevice    `json:"devices"`
	ContainerEdits *containerEdits `json:"containerEdits,omitempty"`
}

type specDevice struct {
	Name           string         `json:"name"`
	ContainerEdits containerEdits `json:"containerEdits"`
}

type containerEdits struct {
	DeviceNodes []deviceNode `json:"deviceNodes,omitempty"`
	Mounts      []mount      `json:"mounts,omitempty"`
	Env         []string     `json:"env,omitempty"`
}

type deviceNode struct {
	Path        string `json:"path"`
	Permissions string `json:"permissions,omitempty"`
}

type mount struct {
	HostPath      string   `json:"hostPath"`
	ContainerPath string   `json:"containerPath"`
	Options       []string `json:"options,omitempty"`
}

// SpecFileName is the on-disk name of the CDI spec for a resource class.
func SpecFileName(class string) string {
	return "tenstorrent-" + class + ".json"
}

// WriteSpec builds the CDI spec for one resource class and writes it atomically
// into dir. Device nodes are per-device; the /sys mount (and hugepages, when
// present at hugepagesPath) are common edits applied to every device of the kind.
func WriteSpec(dir, class string, devs []device.Device, hugepagesPath string) error {
	// CDI mounts of host directories must be explicit bind mounts, otherwise
	// the runtime tries to mount the source as a fresh filesystem and fails
	// ("no such device"). rbind carries submounts (e.g. everything under /sys).
	common := containerEdits{
		Mounts: []mount{{
			HostPath:      "/sys",
			ContainerPath: "/sys",
			Options:       []string{"rbind", "ro"},
		}},
	}
	if hugepagesPath != "" {
		if _, err := os.Stat(hugepagesPath); err == nil {
			common.Mounts = append(common.Mounts, mount{
				HostPath:      hugepagesPath,
				ContainerPath: hugepagesPath,
				Options:       []string{"rbind"},
			})
		}
	}

	s := spec{
		CDIVersion:     specVersion,
		Kind:           Kind(class),
		ContainerEdits: &common,
	}
	for _, d := range devs {
		s.Devices = append(s.Devices, specDevice{
			Name: d.ID,
			ContainerEdits: containerEdits{
				DeviceNodes: []deviceNode{{Path: d.DevPath, Permissions: "rw"}},
			},
		})
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal CDI spec for %s: %w", class, err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create CDI dir %s: %w", dir, err)
	}

	// Atomic write so a runtime never reads a half-written spec.
	file := filepath.Join(dir, SpecFileName(class))
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write CDI spec %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, file); err != nil {
		return fmt.Errorf("install CDI spec %s: %w", file, err)
	}
	return nil
}

// RemoveSpec deletes the CDI spec for a resource class. It is a no-op if the
// file does not exist.
func RemoveSpec(dir, class string) error {
	if err := os.Remove(filepath.Join(dir, SpecFileName(class))); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove CDI spec for %s: %w", class, err)
	}
	return nil
}
