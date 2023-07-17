//go:build darwin

package usage

import (
	iface "github.com/taubyte/go-interfaces/services/seer"
)

func GetCPUUsage() (cpuData iface.Cpu, err error) {
	cpuData = iface.Cpu{
		Total:     0,
		Count:     0,
		User:      0,
		Nice:      0,
		System:    0,
		Idle:      0,
		Iowait:    0,
		Irq:       0,
		Softirq:   0,
		Steal:     0,
		Guest:     0,
		GuestNice: 0,
		StatCount: 0,
	}
	return
}
