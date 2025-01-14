//go:build windows
// +build windows

package runtime

import (
	"github.com/mackerelio/go-osstat/memory"
)

func getTotalAndUsedMemory() (totalWithSwap uint64, usedWithSwap uint64, total uint64, used uint64, err error) {
	mem, err := memory.Get()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	totalWithSwap = mem.VirtualTotal
	usedWithSwap = mem.VirtualTotal - mem.VirtualFree
	total = mem.Total
	used = mem.Used
	return totalWithSwap, usedWithSwap, total, used, nil
}
