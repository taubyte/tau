//go:build !windows

package usage

import (
	"fmt"
	"syscall"

	iface "github.com/taubyte/go-interfaces/services/seer"
)

func GetDiskUsage() (iface.Disk, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return iface.Disk{}, fmt.Errorf("stat on / failed with: %s", err)
	}

	// All fields in statfs_t are unit64
	return iface.Disk{
		Total:     stat.Blocks * uint64(stat.Bsize),
		Free:      stat.Bfree * uint64(stat.Bsize),
		Used:      (stat.Blocks - stat.Bfree) * uint64(stat.Bsize),
		Available: stat.Bavail * uint64(stat.Bsize),
	}, nil
}
