// Package device handles discovery of Tenstorrent accelerators from the host
// filesystem (/dev/tenstorrent, /sys/class/tenstorrent).
package device

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

const (
	devDir    = "/dev/tenstorrent"
	sysfsBase = "/sys/class/tenstorrent"
)

// Device represents a single Tenstorrent accelerator visible on the host.
// All fields are populated at discovery time and never mutated afterward.
type Device struct {
	ID       string // "0", "1", etc.
	DevPath  string // "/dev/tenstorrent/0"
	CardType string // "p100a", "n150", etc.
	Class    string // "blackhole", "n150", "n300", "grayskull"
	NumaNode int64  // from sysfs; -1 if unavailable
	HwmonDir string // resolved hwmon path for temperature checks
	SysfsDir string // "/sys/class/tenstorrent/tenstorrent!0"
}

// Heartbeat reads the ARC microcontroller heartbeat counter from sysfs.
// The value increments continuously; if two consecutive reads return the same
// value the ARC firmware is frozen and the card is unhealthy.
func Heartbeat(d Device) (string, error) {
	return readSysfs(filepath.Join(d.SysfsDir, "tt_heartbeat"))
}

// Temperature reads the current ASIC temperature in millidegrees Celsius
// (e.g. 47875 == 47.875 C) from the hwmon temp1_input sensor.
func Temperature(d Device) (int64, error) {
	return readSysfsInt(filepath.Join(d.HwmonDir, "temp1_input"))
}

// MaxTemperature reads the card's hardware temperature limit in millidegrees
// Celsius from hwmon temp1_max. Returns an error if the card does not expose it.
func MaxTemperature(d Device) (int64, error) {
	return readSysfsInt(filepath.Join(d.HwmonDir, "temp1_max"))
}

// Power reads the current power draw in microwatts (hwmon power1_input).
func Power(d Device) (int64, error) {
	return readSysfsInt(filepath.Join(d.HwmonDir, "power1_input"))
}

// PowerMax reads the power limit in microwatts (hwmon power1_max).
func PowerMax(d Device) (int64, error) {
	return readSysfsInt(filepath.Join(d.HwmonDir, "power1_max"))
}

// Voltage reads the core voltage in millivolts (hwmon in0_input).
func Voltage(d Device) (int64, error) {
	return readSysfsInt(filepath.Join(d.HwmonDir, "in0_input"))
}

// Current reads the current draw in milliamperes (hwmon curr1_input).
func Current(d Device) (int64, error) {
	return readSysfsInt(filepath.Join(d.HwmonDir, "curr1_input"))
}

// Clock reads a clock frequency in MHz for the given domain ("ai", "arc",
// "axi"), i.e. the sysfs files tt_aiclk / tt_arcclk / tt_axiclk.
func Clock(d Device, domain string) (int64, error) {
	return readSysfsInt(filepath.Join(d.SysfsDir, "tt_"+domain+"clk"))
}

// Serial reads the card serial number from sysfs.
func Serial(d Device) (string, error) {
	return readSysfs(filepath.Join(d.SysfsDir, "tt_serial"))
}

// AsicID reads the ASIC identifier from sysfs.
func AsicID(d Device) (string, error) {
	return readSysfs(filepath.Join(d.SysfsDir, "tt_asic_id"))
}

// FwBundle reads the firmware bundle version from sysfs.
func FwBundle(d Device) (string, error) {
	return readSysfs(filepath.Join(d.SysfsDir, "tt_fw_bundle_ver"))
}

// PCIeCorrectableErrors reads the cumulative PCIe AER correctable error count
// (the TOTAL_ERR_COR line of the device's aer_dev_correctable file).
func PCIeCorrectableErrors(d Device) (uint64, error) {
	data, err := readSysfs(filepath.Join(d.SysfsDir, "device", "aer_dev_correctable"))
	if err != nil {
		return 0, err
	}
	return parseAERTotal(data)
}

// PCIeLinkSpeedGTps reads the current PCIe link speed in GT/s (parsed from
// e.g. "8.0 GT/s PCIe").
func PCIeLinkSpeedGTps(d Device) (float64, error) {
	s, err := readSysfs(filepath.Join(d.SysfsDir, "device", "current_link_speed"))
	if err != nil {
		return 0, err
	}
	return parseLinkSpeed(s)
}

// PCIeLinkWidth reads the current PCIe link width (number of lanes).
func PCIeLinkWidth(d Device) (int64, error) {
	return readSysfsInt(filepath.Join(d.SysfsDir, "device", "current_link_width"))
}

// parseAERTotal extracts the TOTAL_ERR_COR value from the multi-line
// aer_dev_correctable file ("<NAME> <count>" per line).
func parseAERTotal(s string) (uint64, error) {
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "TOTAL_ERR_COR" {
			return strconv.ParseUint(fields[1], 10, 64)
		}
	}
	return 0, fmt.Errorf("TOTAL_ERR_COR not found")
}

// parseLinkSpeed parses a PCIe link-speed string like "8.0 GT/s PCIe" into its
// GT/s value.
func parseLinkSpeed(s string) (float64, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty link speed")
	}
	return strconv.ParseFloat(fields[0], 64)
}

// Discover returns devices grouped by resource class using the real host paths.
func Discover() (map[string][]Device, error) {
	return discover(devDir, sysfsBase)
}

func discover(devBase, sysBase string) (map[string][]Device, error) {
	entries, err := filepath.Glob(filepath.Join(devBase, "*"))
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", devBase, err)
	}

	result := make(map[string][]Device)
	for _, entry := range entries {
		name := filepath.Base(entry)
		if !isNumeric(name) {
			continue
		}

		sysfsDir := filepath.Join(sysBase, "tenstorrent!"+name)

		cardType, err := readSysfs(filepath.Join(sysfsDir, "tt_card_type"))
		if err != nil {
			klog.Warningf("Skipping device %s: cannot read card type: %v", name, err)
			continue
		}

		class := cardTypeToClass(cardType)
		if class == "" {
			klog.Warningf("Skipping device %s: unknown card type %q", name, cardType)
			continue
		}

		result[class] = append(result[class], Device{
			ID:       name,
			DevPath:  entry,
			CardType: cardType,
			Class:    class,
			NumaNode: readNumaNode(filepath.Join(sysfsDir, "device", "numa_node")),
			HwmonDir: findHwmonDir(filepath.Join(sysfsDir, "device", "hwmon")),
			SysfsDir: sysfsDir,
		})
	}

	return result, nil
}

var classMap = map[string]string{
	"p100a": "blackhole",
	"p150a": "blackhole",
	"p150b": "blackhole",
	"p150c": "blackhole",
	"p300a": "blackhole",
	"p300b": "blackhole",
	"p300c": "blackhole",
	"n150":  "n150",
	"n300":  "n300",
	"n300l": "n300",
	"n300s": "n300",
	"e75":   "grayskull",
	"e150":  "grayskull",
}

func cardTypeToClass(cardType string) string {
	return classMap[cardType]
}

func readSysfs(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readSysfsInt(path string) (int64, error) {
	s, err := readSysfs(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(s, 10, 64)
}

func readNumaNode(path string) int64 {
	s, err := readSysfs(path)
	if err != nil {
		return -1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

func findHwmonDir(hwmonBase string) string {
	matches, err := filepath.Glob(filepath.Join(hwmonBase, "hwmon*"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
