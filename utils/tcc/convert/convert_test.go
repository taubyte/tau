package convert

import (
	"encoding/json"
	"testing"

	"gotest.tools/v3/assert"
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
	assert.NilError(t, err)
	var eroded map[string]any
	assert.NilError(t, json.Unmarshal(data, &eroded))

	got := normalizeMap(eroded).(map[string]any)

	domains, ok := got["domains"].([]string)
	assert.Assert(t, ok, "domains should be restored to []string, got %T", got["domains"])
	assert.DeepEqual(t, domains, []string{"a", "b"})

	min, ok := got["min"].(int)
	assert.Assert(t, ok, "min should be int, got %T", got["min"])
	assert.Equal(t, min, 30)

	mem, ok := got["memory"].(int64)
	assert.Assert(t, ok, "memory should be int64, got %T", got["memory"])
	assert.Equal(t, mem, int64(20_000_000_000))

	_, ok = got["mixed"].([]any)
	assert.Assert(t, ok, "mixed should stay []any, got %T", got["mixed"])
}
