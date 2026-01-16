package pass4

import (
	"context"
	"testing"

	specs "github.com/taubyte/tau/pkg/specs/function"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestFunctions_PathTooShort(t *testing.T) {
	// Use case: Testing with insufficient context path
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background())
	ctx = ctx.Fork(configRoot)

	transformer := Functions("main")
	_, err := transformer.Process(ctx, configRoot)

	// Verify: Should return error about path being too short
	assert.ErrorContains(t, err, "path")
	assert.ErrorContains(t, err, "too short")
}

func TestFunctions_RootNotObject(t *testing.T) {
	// Use case: Testing with invalid root in context
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background(), "not-an-object", configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Functions("main")
	_, err := transformer.Process(ctx, configRoot)

	// Verify: Should return error about root not being an object
	assert.ErrorContains(t, err, "root is not an object")
}

func TestFunctions_ConfigRootNotObject(t *testing.T) {
	// Use case: Testing with invalid configRoot in context
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, "not-an-object")
	ctx = ctx.Fork(configRoot)

	transformer := Functions("main")
	_, err := transformer.Process(ctx, configRoot)

	// Verify: Should return error about config root not being an object
	assert.ErrorContains(t, err, "config root is not an object")
}

func TestFunctions_ProjectIdNotString(t *testing.T) {
	// Use case: Testing with invalid project ID type
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", 12345) // Not a string

	funcConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	funcSel := funcConfig.Child("func-id-456")
	funcSel.Set("name", "myFunction")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Functions("main")
	_, err := transformer.Process(ctx, configRoot)

	// Verify: Should return error about project id not being a string
	assert.ErrorContains(t, err, "project id is not a string")
}

func TestFunctions_FunctionNameNotString(t *testing.T) {
	// Use case: Testing with invalid function name type
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	funcConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	funcSel := funcConfig.Child("func-id-456")
	funcSel.Set("name", 12345) // Not a string

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Functions("main")
	_, err := transformer.Process(ctx, configRoot)

	// Verify: Should return error about function name not being a string
	assert.ErrorContains(t, err, "function name is not a string")
}
