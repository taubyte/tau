//go:build windows

package usage

import (
	iface "github.com/taubyte/tau/core/services/seer"
)

func GetDiskUsage() (iface.Disk, error) {
	// All fields in statfs_t are unit64
	return iface.Disk{
		Total:     0,
		Free:      0,
		Used:      0,
		Available: 0,
	}, nil
}
