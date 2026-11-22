//go:build linux

package storage

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type mountEntry struct {
	mountPoint string
	fsType     string
	source     string
	options    string
}

// pseudoFS are file systems that never hold a user's library.
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true, "tmpfs": true, "cgroup": true,
	"cgroup2": true, "securityfs": true, "pstore": true, "debugfs": true, "tracefs": true,
	"configfs": true, "fusectl": true, "mqueue": true, "hugetlbfs": true, "autofs": true,
	"binfmt_misc": true, "bpf": true, "rpc_pipefs": true, "nsfs": true, "overlay": true,
	"squashfs": true, "efivarfs": true, "ramfs": true, "fuse.portal": true, "fuse.gvfsd-fuse": true,
}

func readMounts() ([]mountEntry, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []mountEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		// Format: id parent major:minor root mountpoint options [optional...] - fstype source superopts
		line := sc.Text()
		sep := strings.Index(line, " - ")
		if sep < 0 {
			continue
		}
		left := strings.Fields(line[:sep])
		right := strings.Fields(line[sep+3:])
		if len(left) < 6 || len(right) < 2 {
			continue
		}
		out = append(out, mountEntry{
			mountPoint: unescapeMount(left[4]),
			options:    left[5],
			fsType:     right[0],
			source:     unescapeMount(right[1]),
		})
	}
	return out, sc.Err()
}

// unescapeMount decodes the octal escapes the kernel uses for spaces etc.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			v := 0
			ok := true
			for j := 1; j <= 3; j++ {
				c := s[i+j]
				if c < '0' || c > '7' {
					ok = false
					break
				}
				v = v*8 + int(c-'0')
			}
			if ok {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Stat returns information about the volume containing path.
func Stat(path string) (Volume, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Volume{}, err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	var st unix.Statfs_t
	if err := unix.Statfs(abs, &st); err != nil {
		return Volume{}, err
	}
	v := Volume{
		MountPoint: "/",
		TotalBytes: st.Blocks * uint64(st.Bsize),
		AvailBytes: st.Bavail * uint64(st.Bsize),
		FreeBytes:  st.Bfree * uint64(st.Bsize),
		ReadOnly:   st.Flags&unix.ST_RDONLY != 0,
	}
	if mounts, err := readMounts(); err == nil {
		best := -1
		for i, m := range mounts {
			if isUnder(abs, m.mountPoint) && (best < 0 || len(m.mountPoint) >= len(mounts[best].mountPoint)) {
				best = i
			}
		}
		if best >= 0 {
			m := mounts[best]
			v.MountPoint = m.mountPoint
			v.Device = m.source
			v.FSType = NormalizeFSType(m.fsType)
			v.ReadOnly = v.ReadOnly || hasOption(m.options, "ro")
			v.Removable = removableDevice(m.source)
			v.System = m.mountPoint == "/"
			v.Label = labelFor(m.source, m.mountPoint)
		}
	}
	return v, nil
}

// ListVolumes enumerates mounted block-device file systems.
func ListVolumes() ([]Volume, error) {
	mounts, err := readMounts()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Volume
	for _, m := range mounts {
		if pseudoFS[m.fsType] || !strings.HasPrefix(m.source, "/dev/") {
			continue
		}
		if strings.HasPrefix(m.mountPoint, "/boot") || strings.HasPrefix(m.mountPoint, "/snap/") {
			continue
		}
		if seen[m.source] {
			continue // bind mount of a device already listed
		}
		seen[m.source] = true
		v, err := Stat(m.mountPoint)
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func isUnder(path, mount string) bool {
	if mount == "/" {
		return true
	}
	return path == mount || strings.HasPrefix(path, strings.TrimSuffix(mount, "/")+"/")
}

func hasOption(opts, want string) bool {
	for _, o := range strings.Split(opts, ",") {
		if o == want {
			return true
		}
	}
	return false
}

// removableDevice inspects sysfs. USB-attached SSDs frequently report
// removable=0, so a USB or MMC (SD card) bus in the device path also counts.
func removableDevice(source string) *bool {
	if !strings.HasPrefix(source, "/dev/") {
		return nil
	}
	if real, err := filepath.EvalSymlinks(source); err == nil {
		source = real
	}
	name := filepath.Base(source)
	sysPath, err := filepath.EvalSymlinks(filepath.Join("/sys/class/block", name))
	if err != nil {
		return nil
	}
	disk := sysPath
	if _, err := os.Stat(filepath.Join(sysPath, "partition")); err == nil {
		disk = filepath.Dir(sysPath)
	}
	t := true
	f := false
	if strings.Contains(disk, "/usb") || strings.Contains(disk, "/mmc_host/") || strings.Contains(disk, "/memstick") {
		return &t
	}
	b, err := os.ReadFile(filepath.Join(disk, "removable"))
	if err != nil {
		return nil
	}
	if strings.TrimSpace(string(b)) == "1" {
		return &t
	}
	return &f
}

func labelFor(source, mountPoint string) string {
	entries, err := os.ReadDir("/dev/disk/by-label")
	if err == nil {
		for _, e := range entries {
			target, err := filepath.EvalSymlinks(filepath.Join("/dev/disk/by-label", e.Name()))
			if err == nil && target == source {
				return unescapeMount(strings.ReplaceAll(e.Name(), `\x20`, " "))
			}
		}
	}
	if mountPoint != "/" {
		return filepath.Base(mountPoint)
	}
	return ""
}
