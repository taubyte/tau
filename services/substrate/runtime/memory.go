//go:build linux || darwin
// +build linux darwin

package runtime

import (
	"github.com/mackerelio/go-osstat/memory"
)

func getTotalAndUsedMemory() (totalWithSwap uint64, usedWithSwap uint64, total uint64, used uint64, err error) {
	mem, err := memory.Get()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	totalWithSwap = mem.Total + mem.SwapTotal
	usedWithSwap = mem.Used + mem.SwapUsed
	return totalWithSwap, usedWithSwap, mem.Total, mem.Used, nil
}
