package usage

import (
	"fmt"
	"runtime"

	"github.com/mackerelio/go-osstat/memory"
	iface "github.com/taubyte/go-interfaces/services/seer"
)

func GetMemoryUsage() (memData iface.Memory, err error) {
	var stat runtime.MemStats
	runtime.ReadMemStats(&stat)

	memoryStats, err := memory.Get()
	if err != nil {
		err = fmt.Errorf("getting go-osstat/memory usage failed with: %s", err)
		return
	}

	memData = iface.Memory{
		Used:  stat.Sys,
		Total: memoryStats.Total,
		Free:  memoryStats.Free,
	}
	return
}
