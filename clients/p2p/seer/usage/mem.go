package usage

import (
	"fmt"
	"runtime"

	"github.com/shirou/gopsutil/v4/mem"
	iface "github.com/taubyte/tau/core/services/seer"
)

func GetMemoryUsage() (memData iface.Memory, err error) {
	var stat runtime.MemStats
	runtime.ReadMemStats(&stat)

	memoryStats, err := mem.VirtualMemory()
	if err != nil {
		err = fmt.Errorf("getting gopsutil/memory usage failed with: %s", err)
		return
	}

	memData = iface.Memory{
		Used:  stat.Sys,
		Total: memoryStats.Total,
		Free:  memoryStats.Available,
	}
	return
}
