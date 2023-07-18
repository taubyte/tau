package usage

import (
	"fmt"

	iface "github.com/taubyte/go-interfaces/services/seer"
)

func GetUsage() (usage iface.UsageData, err error) {
	memory, err := GetMemoryUsage()
	if err != nil {
		err = fmt.Errorf("getting memory usage failed with: %w", err)
		return
	}

	cpu, err := GetCPUUsage()
	if err != nil {
		err = fmt.Errorf("getting cpu usage failed with: %w", err)
		return
	}

	disk, err := GetDiskUsage()
	if err != nil {
		err = fmt.Errorf("getting disk usage failed with: %w", err)
		return
	}

	usage = iface.UsageData{
		Memory: memory,
		Cpu:    cpu,
		Disk:   disk,
	}

	return
}
