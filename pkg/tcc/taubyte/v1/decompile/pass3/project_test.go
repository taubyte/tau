package pass3

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestProject(t *testing.T) {
	project := Project()

	obj := object.New[object.Refrence]()
	obj.Set("id", "test-id")
	obj.Set("name", "test")

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := project.Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result == obj, "should return same object")
}

func TestPipe(t *testing.T) {
	pipe := Pipe()
	assert.Assert(t, len(pipe) > 0, "pipe should contain transformers")
}
