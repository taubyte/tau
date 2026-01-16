package pass1

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestProject_WithTags(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("tags", []string{"tag1", "tag2"})
	obj.Set("id", "project-id-123")

	transformer := Project()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify tags deleted (compat cleanup)
	tags := result.Get("tags")
	assert.Assert(t, tags == nil)

	// Verify other attributes remain
	id := result.Get("id")
	assert.Equal(t, id.(string), "project-id-123")
}

func TestProject_WithoutTags(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-456")

	transformer := Project()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	// Verify id remains
	id := result.Get("id")
	assert.Equal(t, id.(string), "project-id-456")
}
