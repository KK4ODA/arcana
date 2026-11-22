package storage

import (
	"strings"
	"testing"
)

func TestNominalCapacity(t *testing.T) {
	cases := []struct {
		total uint64
		want  uint64
		ok    bool
	}{
		{1_000_204_886_016, 1_000_000_000_000, true}, // typical "1 TB" disk
		{128_043_712_512, 128_000_000_000, true},     // 128 GB stick
		{123_000_000_000, 128_000_000_000, true},     // formatted 128 GB with overhead
		{2_000_398_934_016, 2_000_000_000_000, true},
		{700_000_000_000, 750_000_000_000, true},
		{50_000_000_000, 0, false},
	}
	for _, c := range cases {
		got, ok := NominalCapacity(c.total)
		if got != c.want || ok != c.ok {
			t.Errorf("NominalCapacity(%d) = %d,%v want %d,%v", c.total, got, ok, c.want, c.ok)
		}
	}
}

func TestFormat(t *testing.T) {
	if got := FormatBytes(1_000_204_886_016); got != "1.0 TB" {
		t.Errorf("FormatBytes = %q", got)
	}
	if got := FormatBinary(1_000_204_886_016); got != "931.5 GiB" {
		t.Errorf("FormatBinary = %q", got)
	}
	if got := FormatBytes(320_000_000_000); got != "320 GB" {
		t.Errorf("FormatBytes = %q", got)
	}
}

func TestPlanExample(t *testing.T) {
	// The example from the design brief: 1 TB drive, 320 GB installed,
	// 180 GB selected, 50 GB reserve.
	const GB = 1_000_000_000
	vol := Volume{TotalBytes: 1000 * GB, AvailBytes: 680 * GB, FSType: "exfat"}
	p := Compute(PlanInput{Volume: vol, Installed: 320 * GB, Selected: 180 * GB, Reserve: 50 * GB})
	if p.AfterSelection != 500*GB {
		t.Errorf("AfterSelection = %d", p.AfterSelection)
	}
	if p.Headroom != 450*GB {
		t.Errorf("Headroom = %d", p.Headroom)
	}
	if p.Status != "ok" {
		t.Errorf("Status = %s", p.Status)
	}
	if p.Other != 0 {
		t.Errorf("Other = %d", p.Other)
	}
}

func TestPlanOverAndTight(t *testing.T) {
	const GB = 1_000_000_000
	vol := Volume{TotalBytes: 128 * GB, AvailBytes: 100 * GB}
	p := Compute(PlanInput{Volume: vol, QueuedRemaining: 40 * GB, Selected: 60 * GB, Reserve: 5 * GB})
	if p.Status != "over" || p.Headroom != -5*GB {
		t.Fatalf("got status %s headroom %d", p.Status, p.Headroom)
	}
	if !strings.Contains(p.Messages[0], "5.0 GB") {
		t.Errorf("message should state the shortfall: %v", p.Messages)
	}
	p = Compute(PlanInput{Volume: vol, Selected: 90 * GB, SelectedUncertain: 40 * GB, Reserve: 5 * GB})
	if p.Status != "tight" {
		t.Fatalf("got status %s, want tight", p.Status)
	}
}

func TestCheckWrite(t *testing.T) {
	if err := CheckWrite(10_000, 5_000, 2_000, 2_000); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := CheckWrite(10_000, 7_000, 2_000, 2_000); err == nil {
		t.Fatal("expected insufficient space")
	}
}

func TestFileSystemRules(t *testing.T) {
	if ok, _ := CanStore("vfat", 5<<30); ok {
		t.Error("FAT32 must reject files over 4 GiB")
	}
	if ok, _ := CanStore("exfat", 100<<30); !ok {
		t.Error("exFAT must accept large files")
	}
	if FileSystemInfo("ntfs3").MacOS != readOnly {
		t.Error("NTFS should be read-only on macOS")
	}
	if NormalizeFSType("fuseblk") != "ntfs" {
		t.Error("fuseblk should normalize to ntfs")
	}
}

func TestStatCurrentDir(t *testing.T) {
	v, err := Stat(t.TempDir())
	if err != nil {
		t.Skipf("Stat unsupported here: %v", err)
	}
	if v.TotalBytes == 0 || v.AvailBytes > v.TotalBytes {
		t.Fatalf("implausible volume: %+v", v)
	}
}
