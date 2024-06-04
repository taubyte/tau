//go:build !(darwin || windows)

package usage

import (
	"fmt"

	"github.com/mackerelio/go-osstat/cpu"
	iface "github.com/taubyte/tau/core/services/seer"
)

func GetCPUUsage() (cpuData iface.Cpu, err error) {
	cpu, err := cpu.Get()
	if err != nil {
		err = fmt.Errorf("getting go-osstat/cpu usage failed with: %s", err)
		return
	}

	cpuData = iface.Cpu{
		Total:     cpu.Total,
		Count:     cpu.CPUCount,
		User:      cpu.User,
		Nice:      cpu.Nice,
		System:    cpu.System,
		Idle:      cpu.Idle,
		Iowait:    cpu.Iowait,
		Irq:       cpu.Irq,
		Softirq:   cpu.Softirq,
		Steal:     cpu.Steal,
		Guest:     cpu.Guest,
		GuestNice: cpu.GuestNice,
		StatCount: cpu.StatCount,
	}

	return
}
