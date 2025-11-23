package seer

import (
	"reflect"
	"testing"
)

func TestUsageData_ToMap(t *testing.T) {
	tests := []struct {
		name     string
		usage    UsageData
		expected map[string]any
	}{
		{
			name: "basic usage data",
			usage: UsageData{
				Memory: Memory{
					Used:  100,
					Total: 1000,
					Free:  900,
				},
				Cpu: Cpu{
					Total:     500,
					Count:     4,
					User:      100,
					Nice:      10,
					System:    50,
					Idle:      300,
					Iowait:    20,
					Irq:       5,
					Softirq:   10,
					Steal:     0,
					Guest:     0,
					GuestNice: 0,
					StatCount: 1,
				},
				Disk: Disk{
					Total:     2000,
					Free:      1500,
					Used:      400,
					Available: 1600,
				},
				CustomValues: map[string]float64{
					"sensor.network.bytes": 123.45,
					"simple":               42.0,
				},
			},
			expected: map[string]any{
				"memory": map[string]any{
					"used":  uint64(100),
					"total": uint64(1000),
					"free":  uint64(900),
				},
				"cpu": map[string]any{
					"total":     uint64(500),
					"count":     4,
					"user":      uint64(100),
					"nice":      uint64(10),
					"system":    uint64(50),
					"idle":      uint64(300),
					"iowait":    uint64(20),
					"irq":       uint64(5),
					"softirq":   uint64(10),
					"steal":     uint64(0),
					"guest":     uint64(0),
					"guestNice": uint64(0),
					"statCount": 1,
				},
				"disk": map[string]any{
					"total":     uint64(2000),
					"free":      uint64(1500),
					"used":      uint64(400),
					"available": uint64(1600),
				},
				"custom": map[string]any{
					"sensor": map[string]any{
						"network": map[string]any{
							"bytes": 123.45,
						},
					},
					"simple": 42.0,
				},
			},
		},
		{
			name: "nil custom values",
			usage: UsageData{
				Memory: Memory{
					Used:  0,
					Total: 0,
					Free:  0,
				},
				Cpu:          Cpu{},
				Disk:         Disk{},
				CustomValues: nil,
			},
			expected: map[string]any{
				"memory": map[string]any{
					"used":  uint64(0),
					"total": uint64(0),
					"free":  uint64(0),
				},
				"cpu": map[string]any{
					"total":     uint64(0),
					"count":     0,
					"user":      uint64(0),
					"nice":      uint64(0),
					"system":    uint64(0),
					"idle":      uint64(0),
					"iowait":    uint64(0),
					"irq":       uint64(0),
					"softirq":   uint64(0),
					"steal":     uint64(0),
					"guest":     uint64(0),
					"guestNice": uint64(0),
					"statCount": 0,
				},
				"disk": map[string]any{
					"total":     uint64(0),
					"free":      uint64(0),
					"used":      uint64(0),
					"available": uint64(0),
				},
			},
		},
		{
			name: "empty custom values map",
			usage: UsageData{
				Memory: Memory{
					Used:  0,
					Total: 0,
					Free:  0,
				},
				Cpu:          Cpu{},
				Disk:         Disk{},
				CustomValues: map[string]float64{},
			},
			expected: map[string]any{
				"memory": map[string]any{
					"used":  uint64(0),
					"total": uint64(0),
					"free":  uint64(0),
				},
				"cpu": map[string]any{
					"total":     uint64(0),
					"count":     0,
					"user":      uint64(0),
					"nice":      uint64(0),
					"system":    uint64(0),
					"idle":      uint64(0),
					"iowait":    uint64(0),
					"irq":       uint64(0),
					"softirq":   uint64(0),
					"steal":     uint64(0),
					"guest":     uint64(0),
					"guestNice": uint64(0),
					"statCount": 0,
				},
				"disk": map[string]any{
					"total":     uint64(0),
					"free":      uint64(0),
					"used":      uint64(0),
					"available": uint64(0),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.usage.ToMap()

			if len(tt.usage.CustomValues) == 0 {
				if _, exists := result["custom"]; exists {
					t.Errorf("ToMap() should not include 'custom' key when CustomValues is empty, but got: %v", result["custom"])
				}
			}

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ToMap() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestConvertCustomValuesToNestedMap(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]float64
		expected map[string]any
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty map",
			input:    map[string]float64{},
			expected: nil,
		},
		{
			name: "simple flat keys",
			input: map[string]float64{
				"key1": 1.0,
				"key2": 2.0,
			},
			expected: map[string]any{
				"key1": 1.0,
				"key2": 2.0,
			},
		},
		{
			name: "single level nesting",
			input: map[string]float64{
				"sensor.value": 123.45,
			},
			expected: map[string]any{
				"sensor": map[string]any{
					"value": 123.45,
				},
			},
		},
		{
			name: "multi-level nesting",
			input: map[string]float64{
				"sensor.network.bytes": 123.45,
			},
			expected: map[string]any{
				"sensor": map[string]any{
					"network": map[string]any{
						"bytes": 123.45,
					},
				},
			},
		},
		{
			name: "mixed flat and nested keys",
			input: map[string]float64{
				"simple":               42.0,
				"sensor.network.bytes": 123.45,
				"other.value":          99.9,
			},
			expected: map[string]any{
				"simple": 42.0,
				"sensor": map[string]any{
					"network": map[string]any{
						"bytes": 123.45,
					},
				},
				"other": map[string]any{
					"value": 99.9,
				},
			},
		},
		{
			name: "shared prefix keys",
			input: map[string]float64{
				"sensor.network.bytes":   123.45,
				"sensor.network.packets": 456.78,
				"sensor.cpu.usage":       89.0,
			},
			expected: map[string]any{
				"sensor": map[string]any{
					"network": map[string]any{
						"bytes":   123.45,
						"packets": 456.78,
					},
					"cpu": map[string]any{
						"usage": 89.0,
					},
				},
			},
		},
		{
			name: "deep nesting",
			input: map[string]float64{
				"a.b.c.d.e": 1.0,
			},
			expected: map[string]any{
				"a": map[string]any{
					"b": map[string]any{
						"c": map[string]any{
							"d": map[string]any{
								"e": 1.0,
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertCustomValuesToNestedMap(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("convertCustomValuesToNestedMap() = %v, want %v", result, tt.expected)
			}
		})
	}
}
