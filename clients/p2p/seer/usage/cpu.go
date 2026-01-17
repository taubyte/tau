package usage

import (
	"fmt"

	"github.com/shirou/gopsutil/v4/cpu"
	iface "github.com/taubyte/tau/core/services/seer"
)

func GetCPUUsage() (cpuData iface.Cpu, err error) {
	times, err := cpu.Times(true)
	if err != nil {
		err = fmt.Errorf("getting gopsutil/cpu usage failed with: %s", err)
		return
	}

	if len(times) == 0 {
		err = fmt.Errorf("no CPU times available")
		return
	}

	// Aggregate all CPU times
	var ct cpu.TimesStat
	for _, time := range times {
		ct.User += time.User
		ct.Nice += time.Nice
		ct.System += time.System
		ct.Idle += time.Idle
		ct.Iowait += time.Iowait
		ct.Irq += time.Irq
		ct.Softirq += time.Softirq
		ct.Steal += time.Steal
		ct.Guest += time.Guest
		ct.GuestNice += time.GuestNice
	}

	total := ct.User + ct.Nice + ct.System + ct.Idle + ct.Iowait + ct.Irq + ct.Softirq + ct.Steal + ct.Guest + ct.GuestNice

	cpuData = iface.Cpu{
		Total:     uint64(total),
		Count:     len(times),
		User:      uint64(ct.User),
		Nice:      uint64(ct.Nice),
		System:    uint64(ct.System),
		Idle:      uint64(ct.Idle),
		Iowait:    uint64(ct.Iowait),
		Irq:       uint64(ct.Irq),
		Softirq:   uint64(ct.Softirq),
		Steal:     uint64(ct.Steal),
		Guest:     uint64(ct.Guest),
		GuestNice: uint64(ct.GuestNice),
		StatCount: len(times),
	}

	return
}
