package pass2

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestPipe_ReturnsAllTransformers(t *testing.T) {
	// Setup: Create a config object
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-123")

	// Execute: Get pipe transformers
	transformers := Pipe()

	// Verify: Should contain Functions, Smartops, and Websites transformers
	assert.Equal(t, len(transformers), 3)

	// Execute transformers to verify they work
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)

	// All transformers should process without error on empty config
	for _, transformer := range transformers {
		_, err := transformer.Process(ctx, obj)
		assert.NilError(t, err)
	}
}
