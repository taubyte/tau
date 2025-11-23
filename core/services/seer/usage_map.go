package seer

import "strings"

func (u *UsageData) ToMap() map[string]any {
	result := map[string]any{
		"memory": map[string]any{
			"used":  u.Memory.Used,
			"total": u.Memory.Total,
			"free":  u.Memory.Free,
		},
		"cpu": map[string]any{
			"total":     u.Cpu.Total,
			"count":     u.Cpu.Count,
			"user":      u.Cpu.User,
			"nice":      u.Cpu.Nice,
			"system":    u.Cpu.System,
			"idle":      u.Cpu.Idle,
			"iowait":    u.Cpu.Iowait,
			"irq":       u.Cpu.Irq,
			"softirq":   u.Cpu.Softirq,
			"steal":     u.Cpu.Steal,
			"guest":     u.Cpu.Guest,
			"guestNice": u.Cpu.GuestNice,
			"statCount": u.Cpu.StatCount,
		},
		"disk": map[string]any{
			"total":     u.Disk.Total,
			"free":      u.Disk.Free,
			"used":      u.Disk.Used,
			"available": u.Disk.Available,
		},
	}

	custom := convertCustomValuesToNestedMap(u.CustomValues)
	if custom != nil {
		result["custom"] = custom
	}

	return result
}

/*
convertCustomValuesToNestedMap converts a flat map with dot-separated keys
into a nested map structure where '.' is treated as a hierarchical separator.
For example: {"sensor.network.bytes": 123.45} becomes
{"sensor": {"network": {"bytes": 123.45}}}
*/
func convertCustomValuesToNestedMap(customValues map[string]float64) map[string]any {
	if len(customValues) == 0 {
		return nil
	}

	result := make(map[string]any)
	for key, value := range customValues {
		parts := strings.Split(key, ".")
		cur := result
		for i, part := range parts {
			if i == len(parts)-1 {
				cur[part] = value
			} else {
				if next, ok := cur[part]; ok {
					if nextMap, ok := next.(map[string]any); ok {
						cur = nextMap
					} else {
						newMap := make(map[string]any)
						cur[part] = newMap
						cur = newMap
					}
				} else {
					newMap := make(map[string]any)
					cur[part] = newMap
					cur = newMap
				}
			}
		}
	}

	return result
}
