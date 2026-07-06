package convert

import (
	"encoding/json"
	"testing"
)

// After a JSON round-trip (as at the wasm/TNS boundary) []string erodes to
// []any and int erodes to float64; normalizeMap must restore them so the
// decompiler's []string / int assertions hold.
func TestNormalizeRestoresErodedTypes(t *testing.T) {
	src := map[string]any{
		"domains": []string{"a", "b"},
		"min":     30,                    // small -> int (Int-attribute validators need int)
		"memory":  int64(20_000_000_000), // > int32 -> int64 (timeout/memory in ns)
		"mixed":   []any{"x", 1},         // not all strings -> stays []any
	}
	data, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	var eroded map[string]any
	if err := json.Unmarshal(data, &eroded); err != nil {
		t.Fatal(err)
	}

	got := normalizeMap(eroded).(map[string]any)

	if d, ok := got["domains"].([]string); !ok || len(d) != 2 || d[0] != "a" {
		t.Errorf("domains: want []string{a,b}, got %#v", got["domains"])
	}
	if mn, ok := got["min"].(int); !ok || mn != 30 {
		t.Errorf("min: want int 30, got %#v", got["min"])
	}
	if mem, ok := got["memory"].(int64); !ok || mem != 20_000_000_000 {
		t.Errorf("memory: want int64 20000000000, got %#v", got["memory"])
	}
	if _, ok := got["mixed"].([]any); !ok {
		t.Errorf("mixed: want []any, got %T", got["mixed"])
	}
}
