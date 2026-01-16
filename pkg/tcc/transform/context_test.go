package transform

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

func TestNewContext_WithRoot(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	ctx := NewContext[object.Refrence](context.Background(), root)

	// Verify path contains root
	path := ctx.Path()
	assert.Equal(t, len(path), 1)
	assert.Assert(t, path[0] == root)
}

func TestNewContext_WithoutRoot(t *testing.T) {
	ctx := NewContext[object.Refrence](context.Background())

	// Verify path is empty
	path := ctx.Path()
	assert.Equal(t, len(path), 0)
}

func TestContext_Fork(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	app := object.New[object.Refrence]()
	app.Set("name", "app")

	ctx := NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(app)

	// Verify path contains both root and app
	path := ctx.Path()
	assert.Equal(t, len(path), 2)
	assert.Assert(t, path[0] == root)
	assert.Assert(t, path[1] == app)
}

func TestContext_ForkMultiple(t *testing.T) {
	root := object.New[object.Refrence]()
	app := object.New[object.Refrence]()
	resource := object.New[object.Refrence]()

	ctx := NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(app)
	ctx = ctx.Fork(resource)

	path := ctx.Path()
	assert.Equal(t, len(path), 3)
	assert.Assert(t, path[0] == root)
	assert.Assert(t, path[1] == app)
	assert.Assert(t, path[2] == resource)
}

func TestContext_Store(t *testing.T) {
	ctx := NewContext[object.Refrence](context.Background())

	store := ctx.Store()
	assert.Assert(t, store != nil)

	// Verify store operations work
	_, err := store.String("test").Set("value")
	assert.NilError(t, err)

	assert.Assert(t, store.String("test").Exist())
	assert.Equal(t, store.String("test").Get(), "value")
}

func TestContext_StoreSharedAcrossForks(t *testing.T) {
	root := object.New[object.Refrence]()
	app := object.New[object.Refrence]()

	ctx := NewContext[object.Refrence](context.Background(), root)

	// Set value in store
	ctx.Store().String("shared").Set("value")

	// Fork context
	ctxForked := ctx.Fork(app)

	// Verify store is shared
	assert.Assert(t, ctxForked.Store().String("shared").Exist())
	assert.Equal(t, ctxForked.Store().String("shared").Get(), "value")
}
