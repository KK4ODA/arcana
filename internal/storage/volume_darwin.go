//go:build darwin

package storage

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func fromStatfs(st *unix.Statfs_t) Volume {
	mnt := unix.ByteSliceToString(st.Mntonname[:])
	v := Volume{
		MountPoint: mnt,
		Device:     unix.ByteSliceToString(st.Mntfromname[:]),
		FSType:     NormalizeFSType(unix.ByteSliceToString(st.Fstypename[:])),
		TotalBytes: st.Blocks * uint64(st.Bsize),
		AvailBytes: st.Bavail * uint64(st.Bsize),
		FreeBytes:  st.Bfree * uint64(st.Bsize),
		ReadOnly:   st.Flags&unix.MNT_RDONLY != 0,
		System:     mnt == "/" || mnt == "/System/Volumes/Data",
	}
	// Precise removability needs DiskArbitration (cgo). Volumes mounted
	// under /Volumes are external or secondary disks in practice.
	if strings.HasPrefix(mnt, "/Volumes/") {
		t := true
		v.Removable = &t
		v.Label = filepath.Base(mnt)
	}
	return v
}

// Stat returns information about the volume containing path.
func Stat(path string) (Volume, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Volume{}, err
	}
	var st unix.Statfs_t
	if err := unix.Statfs(abs, &st); err != nil {
		return Volume{}, err
	}
	return fromStatfs(&st), nil
}

// ListVolumes enumerates local mounted file systems.
func ListVolumes() ([]Volume, error) {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	buf := make([]unix.Statfs_t, n)
	n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	var out []Volume
	for i := 0; i < n; i++ {
		st := &buf[i]
		if st.Flags&unix.MNT_LOCAL == 0 {
			continue
		}
		fs := unix.ByteSliceToString(st.Fstypename[:])
		if fs == "devfs" || fs == "autofs" || fs == "nullfs" {
			continue
		}
		mnt := unix.ByteSliceToString(st.Mntonname[:])
		if strings.HasPrefix(mnt, "/System/Volumes/") && mnt != "/System/Volumes/Data" {
			continue
		}
		out = append(out, fromStatfs(st))
	}
	return out, nil
}
