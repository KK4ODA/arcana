//go:build windows

package storage

import (
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ioctlStorageQueryProperty = 0x002D1400
	busTypeUsb                = 0x7
	busTypeSd                 = 0xC
	busTypeMmc                = 0xD
	fileReadOnlyVolume        = 0x00080000
)

func init() {
	// Prevent Windows from showing "There is no disk in the drive" dialogs
	// when an empty card reader is probed, or when the library drive is
	// pulled while Arcana is running.
	windows.SetErrorMode(windows.SEM_FAILCRITICALERRORS | windows.SEM_NOOPENFILEERRORBOX)
}

// Stat returns information about the volume containing path.
func Stat(path string) (Volume, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Volume{}, err
	}
	p, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return Volume{}, err
	}
	buf := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(p, &buf[0], uint32(len(buf))); err != nil {
		return Volume{}, err
	}
	root := windows.UTF16ToString(buf)
	return statRoot(root)
}

func statRoot(root string) (Volume, error) {
	rootPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return Volume{}, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(rootPtr, &avail, &total, &free); err != nil {
		return Volume{}, err
	}
	v := Volume{MountPoint: root, TotalBytes: total, AvailBytes: avail, FreeBytes: free}

	label := make([]uint16, windows.MAX_PATH+1)
	fsName := make([]uint16, windows.MAX_PATH+1)
	var serial, maxComp, flags uint32
	if err := windows.GetVolumeInformation(rootPtr, &label[0], uint32(len(label)), &serial, &maxComp, &flags,
		&fsName[0], uint32(len(fsName))); err == nil {
		v.Label = windows.UTF16ToString(label)
		v.FSType = NormalizeFSType(windows.UTF16ToString(fsName))
		v.ReadOnly = flags&fileReadOnlyVolume != 0
	}

	driveType := windows.GetDriveType(rootPtr)
	t, f := true, false
	switch driveType {
	case windows.DRIVE_REMOVABLE:
		v.Removable = &t
	case windows.DRIVE_FIXED:
		// USB hard disks and SSDs report DRIVE_FIXED; ask the storage stack
		// which bus the device sits on.
		if usb, ok := isUSBVolume(root); ok && usb {
			v.Removable = &t
		} else if ok {
			v.Removable = &f
		}
	case windows.DRIVE_CDROM:
		v.Removable = &t
		v.ReadOnly = true
	}
	if sysDir, err := windows.GetSystemDirectory(); err == nil {
		v.System = strings.EqualFold(filepath.VolumeName(sysDir)+`\`, root)
	}
	return v, nil
}

// isUSBVolume queries STORAGE_DEVICE_DESCRIPTOR.BusType for the volume.
// No administrator rights are needed because the handle is opened with zero
// access rights, which IOCTL_STORAGE_QUERY_PROPERTY permits.
func isUSBVolume(root string) (bool, bool) {
	letter := strings.TrimSuffix(root, `\`)
	if len(letter) != 2 || letter[1] != ':' {
		return false, false
	}
	devPath, err := windows.UTF16PtrFromString(`\\.\` + letter)
	if err != nil {
		return false, false
	}
	h, err := windows.CreateFile(devPath, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return false, false
	}
	defer windows.CloseHandle(h)
	// STORAGE_PROPERTY_QUERY{PropertyId: StorageDeviceProperty(0), QueryType: PropertyStandardQuery(0)}
	query := [12]byte{}
	out := make([]byte, 1024)
	var returned uint32
	if err := windows.DeviceIoControl(h, ioctlStorageQueryProperty, &query[0], uint32(len(query)),
		&out[0], uint32(len(out)), &returned, nil); err != nil || returned < 32 {
		return false, false
	}
	bus := *(*uint32)(unsafe.Pointer(&out[28]))
	return bus == busTypeUsb || bus == busTypeSd || bus == busTypeMmc, true
}

// ListVolumes enumerates drive letters with media present.
func ListVolumes() ([]Volume, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, err
	}
	var out []Volume
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		rootPtr, _ := windows.UTF16PtrFromString(root)
		switch windows.GetDriveType(rootPtr) {
		case windows.DRIVE_NO_ROOT_DIR, windows.DRIVE_UNKNOWN, windows.DRIVE_REMOTE:
			continue
		}
		v, err := statRoot(root)
		if err != nil {
			continue // e.g. empty card reader slot
		}
		out = append(out, v)
	}
	return out, nil
}
