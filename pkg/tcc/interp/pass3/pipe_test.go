package pass3

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestPipe_ReturnsChrootTransformer(t *testing.T) {
	// Setup: Create a config object
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-123")

	// Execute: Get pipe transformers
	transformers := Pipe()

	// Verify: Should contain Chroot transformer wrapped in Global
	assert.Equal(t, len(transformers), 1)

	// Execute the transformer to verify it works
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := transformers[0].Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	// Verify object wrapped
	_, err = result.Child("object").Object()
	assert.NilError(t, err)
}
