package decompile

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestChrootUnwrap_WithObjectWrapper(t *testing.T) {
	root := object.New[object.Refrence]()
	wrapped := object.New[object.Refrence]()
	wrapped.Set("id", "test-id")
	wrapped.Set("name", "test")
	err := root.Child("object").Add(wrapped)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := (&chrootUnwrap{}).Process(ctx, root)
	assert.NilError(t, err)

	id, err := result.GetString("id")
	assert.NilError(t, err)
	assert.Equal(t, id, "test-id")
}

func TestChrootUnwrap_WithoutObjectWrapper(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("id", "test-id")
	obj.Set("name", "test")

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := (&chrootUnwrap{}).Process(ctx, obj)
	assert.NilError(t, err)

	id, err := result.GetString("id")
	assert.NilError(t, err)
	assert.Equal(t, id, "test-id")
}
