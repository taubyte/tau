package pass1

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestChroot_WithObjectWrapper(t *testing.T) {
	chroot := Chroot()

	// Create object with "object" wrapper
	root := object.New[object.Refrence]()
	wrapped := object.New[object.Refrence]()
	wrapped.Set("id", "test-id")
	wrapped.Set("name", "test")
	err := root.Child("object").Add(wrapped)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := chroot.Process(ctx, root)
	assert.NilError(t, err)

	// Should return the unwrapped object
	id, err := result.GetString("id")
	assert.NilError(t, err)
	assert.Equal(t, id, "test-id")
}

func TestChroot_WithoutObjectWrapper(t *testing.T) {
	chroot := Chroot()

	// Create object without "object" wrapper
	obj := object.New[object.Refrence]()
	obj.Set("id", "test-id")
	obj.Set("name", "test")

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := chroot.Process(ctx, obj)
	assert.NilError(t, err)

	// Should return the same object
	id, err := result.GetString("id")
	assert.NilError(t, err)
	assert.Equal(t, id, "test-id")
}

func TestChroot_ErrorOnUnwrap(t *testing.T) {
	// Create object with "object" child that exists but fails to unwrap
	// This is tricky - we need to actually create a scenario where Object() fails
	// For now, skip this test as it's hard to simulate without mocking
	t.Skip("Skipping - hard to simulate Object() error without mocking")
}

func TestPipe(t *testing.T) {
	pipe := Pipe()
	assert.Assert(t, len(pipe) > 0, "pipe should contain transformers")
}
