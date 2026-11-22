// Package storage detects volumes, reports capacity and file-system
// characteristics, and computes storage plans that keep downloads from
// exhausting a library drive.
package storage

import (
	"fmt"
	"math"
	"strings"
)

// Volume describes a mounted file system as reported by the host OS.
type Volume struct {
	// MountPoint is the root of the volume ("E:\\", "/media/user/ARCANA").
	MountPoint string `json:"mountPoint"`
	// Device is the OS device identifier when known ("/dev/sdb1").
	Device string `json:"device,omitempty"`
	Label  string `json:"label,omitempty"`
	// FSType is a normalized, lower-case file-system name: exfat, ntfs,
	// fat32, ext4, apfs, hfs, btrfs, xfs, f2fs, ...
	FSType string `json:"fsType"`
	// TotalBytes is the formatted capacity the OS reports.
	TotalBytes uint64 `json:"totalBytes"`
	// AvailBytes is the space this (unprivileged) user may still write.
	AvailBytes uint64 `json:"availBytes"`
	// FreeBytes includes blocks reserved for the superuser on ext4.
	FreeBytes uint64 `json:"freeBytes"`
	// Removable is true for USB/SD/card-reader devices, false for internal
	// disks, and nil when the platform cannot tell.
	Removable *bool `json:"removable"`
	ReadOnly  bool  `json:"readOnly"`
	// System is true for the volume the OS boots from.
	System bool `json:"system"`
}

// FSInfo summarizes properties of a file system that matter for a portable
// knowledge library.
type FSInfo struct {
	Name string `json:"name"`
	// MaxFileSize is the largest single file the file system can hold, or 0
	// when the limit is far beyond any realistic resource.
	MaxFileSize uint64 `json:"maxFileSize"`
	Journaled   bool   `json:"journaled"`
	Windows     string `json:"windows"`
	MacOS       string `json:"macos"`
	Linux       string `json:"linux"`
	// Warnings are shown to the user next to the volume.
	Warnings []string `json:"warnings,omitempty"`
}

const (
	native   = "read/write"
	readOnly = "read-only"
	driver   = "needs third-party driver"
)

// FileSystemInfo returns portability and durability notes for fsType.
func FileSystemInfo(fsType string) FSInfo {
	switch NormalizeFSType(fsType) {
	case "exfat":
		return FSInfo{Name: "exFAT", Journaled: false, Windows: native, MacOS: native, Linux: native,
			Warnings: []string{"exFAT has no journal: always eject the drive before unplugging it. Arcana's own metadata is written transactionally, but other files on the drive are at risk."}}
	case "ntfs":
		return FSInfo{Name: "NTFS", Journaled: true, Windows: native, MacOS: readOnly, Linux: native,
			Warnings: []string{"macOS can only read NTFS: downloads and settings changes will not work when this drive is used on a Mac."}}
	case "fat32":
		return FSInfo{Name: "FAT32", MaxFileSize: 1<<32 - 1, Journaled: false, Windows: native, MacOS: native, Linux: native,
			Warnings: []string{"FAT32 cannot store files larger than 4 GiB. Most large reference archives exceed that size; reformat the drive as exFAT to use them."}}
	case "ext4":
		return FSInfo{Name: "ext4", Journaled: true, Windows: driver, MacOS: driver, Linux: native,
			Warnings: []string{"Windows and macOS cannot read ext4 without extra software. Suitable for a dedicated Linux appliance."}}
	case "apfs":
		return FSInfo{Name: "APFS", Journaled: true, Windows: driver, MacOS: native, Linux: driver,
			Warnings: []string{"Only macOS can use APFS natively."}}
	case "hfs":
		return FSInfo{Name: "HFS+", Journaled: true, Windows: driver, MacOS: native, Linux: readOnly,
			Warnings: []string{"HFS+ is not writable on Windows or (by default) Linux."}}
	case "btrfs", "xfs", "f2fs":
		name := NormalizeFSType(fsType)
		return FSInfo{Name: name, Journaled: true, Windows: driver, MacOS: driver, Linux: native,
			Warnings: []string{"Only Linux can use this file system natively."}}
	case "":
		return FSInfo{Name: "unknown"}
	default:
		return FSInfo{Name: fsType}
	}
}

// NormalizeFSType maps OS-specific names onto Arcana's vocabulary.
func NormalizeFSType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "vfat", "msdos", "fat", "fat32", "fat16":
		return "fat32"
	case "ntfs", "ntfs3", "ntfs-3g", "fuseblk", "ufsd_ntfs", "tuxera_ntfs":
		// fuseblk is almost always ntfs-3g on desktop Linux.
		return "ntfs"
	case "exfat", "fuse.exfat", "exfat-fuse":
		return "exfat"
	case "hfs", "hfsplus":
		return "hfs"
	}
	return t
}

// CanStore reports whether a file of size bytes fits the file system's
// per-file limit, with a human-readable reason when it does not.
func CanStore(fsType string, size int64) (bool, string) {
	info := FileSystemInfo(fsType)
	if info.MaxFileSize > 0 && size > 0 && uint64(size) > info.MaxFileSize {
		return false, fmt.Sprintf("%s cannot store a single %s file (limit %s)", info.Name, FormatBytes(size), FormatBytes(int64(info.MaxFileSize)))
	}
	return true, ""
}

// nominalSizesGB are capacities drives are commonly sold as, in decimal GB.
var nominalSizesGB = []float64{1, 2, 4, 8, 16, 32, 64, 120, 128, 240, 250, 256, 320, 480, 500, 512,
	640, 750, 960, 1000, 1500, 2000, 3000, 4000, 5000, 6000, 8000, 10000, 12000, 14000, 16000, 18000, 20000, 22000, 24000}

// NominalCapacity estimates the capacity printed on the drive's packaging.
// Manufacturers use decimal units (1 TB = 10^12 bytes) and formatting consumes
// a little space, so the OS reports less than the label. The second return
// value is false when no common size is a plausible match.
func NominalCapacity(totalBytes uint64) (uint64, bool) {
	gb := float64(totalBytes) / 1e9
	for _, n := range nominalSizesGB {
		// Formatted capacity is typically 93-100% of the nominal size.
		if gb <= n*1.005 && gb >= n*0.90 {
			return uint64(n * 1e9), true
		}
	}
	return 0, false
}

// FormatBytes renders a byte count in decimal units, as drive vendors and
// macOS do.
func FormatBytes(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	units := []string{"B", "kB", "MB", "GB", "TB", "PB"}
	v := float64(n)
	i := 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	s := ""
	switch {
	case i == 0:
		s = fmt.Sprintf("%d B", n)
	case v >= 100:
		s = fmt.Sprintf("%.0f %s", math.Round(v), units[i])
	default:
		s = fmt.Sprintf("%.1f %s", v, units[i])
	}
	if neg {
		return "-" + s
	}
	return s
}

// FormatBinary renders a byte count in binary units (GiB), as Windows does
// even though it labels them "GB".
func FormatBinary(n int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
