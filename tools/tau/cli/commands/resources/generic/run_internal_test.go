package generic

import (
	"testing"
	"time"

	"github.com/taubyte/tau/tools/tau/tcc"
	"gotest.tools/v3/assert"
)

// runSpec projects the slice of a function document the local runner needs,
// parsing the DSL's human forms.
// A LOCAL RUN NEEDS THE ENTRYPOINT AND THE LIMITS, and nothing else. A function
// declares no trigger in v2 — how it is reached is an inbound edge's business —
// so the request served is the one the caller typed, not one the config
// predicted.
func TestRunSpec(t *testing.T) {
	doc := tcc.Doc{
		"id": "QmX",
		"execution": map[string]any{
			"call":    "handle",
			"memory":  "16MB",
			"timeout": "30s",
		},
	}
	spec, err := runSpec("fn", doc)
	assert.NilError(t, err)
	assert.Equal(t, spec.Name, "fn")
	assert.Equal(t, spec.Call, "handle")
	assert.Equal(t, spec.Memory, uint64(16*1024*1024))
	assert.Equal(t, spec.Timeout, uint64(30*time.Second))
}

// A function with no entrypoint cannot be run, and saying so beats loading a
// wasm and calling nothing.
func TestRunSpecNeedsAnEntrypoint(t *testing.T) {
	_, err := runSpec("fn", tcc.Doc{"execution": map[string]any{"memory": "16MB"}})
	assert.ErrorContains(t, err, "entrypoint")
}

func TestRunSpecBadScalars(t *testing.T) {
	_, err := runSpec("fn", tcc.Doc{
		"execution": map[string]any{"call": "handle", "memory": "banana"},
	})
	assert.ErrorContains(t, err, "memory")

	_, err = runSpec("fn", tcc.Doc{
		"execution": map[string]any{"call": "handle", "timeout": "20x"},
	})
	assert.ErrorContains(t, err, "timeout")
}
