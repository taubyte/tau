package generic

import (
	"testing"
	"time"

	"github.com/taubyte/tau/tools/tau/tcc"
	"gotest.tools/v3/assert"
)

// runSpec projects the slice of a function document the local runner needs,
// parsing the DSL's human forms.
func TestRunSpec(t *testing.T) {
	doc := tcc.Doc{
		"id": "QmX",
		"trigger": map[string]any{
			"type":    "https",
			"method":  "POST",
			"paths":   []any{"/a", "/b"},
			"domains": []any{"d1"},
		},
		"execution": map[string]any{
			"call":    "handle",
			"memory":  "16MB",
			"timeout": "30s",
		},
	}
	spec, err := runSpec("fn", doc)
	assert.NilError(t, err)
	assert.Equal(t, spec.Name, "fn")
	assert.Equal(t, spec.Type, "https")
	assert.Equal(t, spec.Method, "POST")
	assert.DeepEqual(t, spec.Paths, []string{"/a", "/b"})
	assert.DeepEqual(t, spec.Domains, []string{"d1"})
	assert.Equal(t, spec.Call, "handle")
	assert.Equal(t, spec.Memory, uint64(16*1024*1024))
	assert.Equal(t, spec.Timeout, uint64(30*time.Second))
}

func TestRunSpecRejectsNonHTTP(t *testing.T) {
	_, err := runSpec("fn", tcc.Doc{"trigger": map[string]any{"type": "pubsub"}})
	assert.ErrorContains(t, err, "HTTP(S)")
}

func TestRunSpecBadScalars(t *testing.T) {
	_, err := runSpec("fn", tcc.Doc{
		"trigger":   map[string]any{"type": "http"},
		"execution": map[string]any{"memory": "banana"},
	})
	assert.ErrorContains(t, err, "memory")

	_, err = runSpec("fn", tcc.Doc{
		"trigger":   map[string]any{"type": "http"},
		"execution": map[string]any{"timeout": "20x"},
	})
	assert.ErrorContains(t, err, "timeout")
}
