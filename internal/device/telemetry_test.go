package device

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAERTotal(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    uint64
		wantErr bool
	}{
		{"typical", "RxErr 0\nBadTLP 2\nTOTAL_ERR_COR 5\n", 5, false},
		{"zero", "TOTAL_ERR_COR 0", 0, false},
		{"missing", "RxErr 0\nBadTLP 0\n", 0, true},
		{"malformed value", "TOTAL_ERR_COR notanumber", 0, true},
		{"empty", "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAERTotal(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseLinkSpeed(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    float64
		wantErr bool
	}{
		{"gen3", "8.0 GT/s PCIe", 8.0, false},
		{"gen1", "2.5 GT/s", 2.5, false},
		{"malformed", "unknown", 0, true},
		{"empty", "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLinkSpeed(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTelemetryReaders exercises the sysfs readers end-to-end over a fake tree.
func TestTelemetryReaders(t *testing.T) {
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
	write(hwmon, "power1_input", "15000000\n")
	write(hwmon, "power1_max", "100000000\n")
	write(hwmon, "in0_input", "795\n")
	write(hwmon, "curr1_input", "18000\n")
	write(root, "tt_aiclk", "500\n")
	write(root, "tt_serial", "010001851180B42E\n")
	write(pciDev, "aer_dev_correctable", "RxErr 0\nTOTAL_ERR_COR 0\n")
	write(pciDev, "current_link_speed", "8.0 GT/s PCIe\n")
	write(pciDev, "current_link_width", "16\n")

	dev := Device{HwmonDir: hwmon, SysfsDir: root}

	if v, err := Power(dev); err != nil || v != 15000000 {
		t.Errorf("Power = %d, %v", v, err)
	}
	if v, err := PowerMax(dev); err != nil || v != 100000000 {
		t.Errorf("PowerMax = %d, %v", v, err)
	}
	if v, err := Voltage(dev); err != nil || v != 795 {
		t.Errorf("Voltage = %d, %v", v, err)
	}
	if v, err := Current(dev); err != nil || v != 18000 {
		t.Errorf("Current = %d, %v", v, err)
	}
	if v, err := Clock(dev, "ai"); err != nil || v != 500 {
		t.Errorf("Clock(ai) = %d, %v", v, err)
	}
	if v, err := Serial(dev); err != nil || v != "010001851180B42E" {
		t.Errorf("Serial = %q, %v", v, err)
	}
	if v, err := PCIeCorrectableErrors(dev); err != nil || v != 0 {
		t.Errorf("PCIeCorrectableErrors = %d, %v", v, err)
	}
	if v, err := PCIeLinkSpeedGTps(dev); err != nil || v != 8.0 {
		t.Errorf("PCIeLinkSpeedGTps = %v, %v", v, err)
	}
	if v, err := PCIeLinkWidth(dev); err != nil || v != 16 {
		t.Errorf("PCIeLinkWidth = %d, %v", v, err)
	}

	// A missing sensor must surface an error (so callers omit the series).
	if _, err := Clock(dev, "arc"); err == nil {
		t.Error("Clock(arc) expected error for absent sysfs file")
	}
}
