package utils

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestIndexById_IndexesResource(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	obj := object.New[object.Refrence]()
	obj.Set("name", "project")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	err := IndexById(ctx, "functions", "myFunction", "func-id-123")

	assert.NilError(t, err)

	// Verify indexed (path is built from obj's name: "project/functions/myFunction")
	indexPath := "project/functions/myFunction"
	assert.Assert(t, ctx.Store().String(indexPath).Exist())
	assert.Equal(t, ctx.Store().String(indexPath).Get(), "func-id-123")
}

func TestResolveNameToId_LocalResolution(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	obj := object.New[object.Refrence]()
	obj.Set("name", "project")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	// Index resource (path: "project/domains/myDomain")
	err := IndexById(ctx, "domains", "myDomain", "domain-id-456")
	assert.NilError(t, err)

	// Resolve name to ID
	id, err := ResolveNameToId(ctx, "domains", "myDomain")

	assert.NilError(t, err)
	assert.Equal(t, id, "domain-id-456")
}

func TestResolveNameToId_GlobalFallback(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	app := object.New[object.Refrence]()
	app.Set("name", "app")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	// Index at root level
	// Path is [root], so ctp[1:] is [] -> "libraries/myLib"
	err := IndexById(ctx, "libraries", "myLib", "lib-id-789")
	assert.NilError(t, err)

	// Verify it was indexed at the expected path
	indexPath := "libraries/myLib"
	assert.Assert(t, ctx.Store().String(indexPath).Exist())
	assert.Equal(t, ctx.Store().String(indexPath).Get(), "lib-id-789")

	// Fork to app context (path is now [root, app])
	ctxApp := ctx.Fork(app)

	// Resolve should fallback to global
	// First tries local: path [root, app], ctp[1:] is [app] -> "app/libraries/myLib" (doesn't exist)
	// Then falls back to global: ctp[:1] is [root], ctp[1:] is [] -> "libraries/myLib" (exists)
	id, err := ResolveNameToId(ctxApp, "libraries", "myLib")

	assert.NilError(t, err)
	assert.Equal(t, id, "lib-id-789")
}

func TestResolveNamesToId_MultipleNames(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	obj := object.New[object.Refrence]()
	obj.Set("name", "project")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	// Index multiple resources
	IndexById(ctx, "domains", "domain1", "domain-id-1")
	IndexById(ctx, "domains", "domain2", "domain-id-2")
	IndexById(ctx, "domains", "domain3", "domain-id-3")

	// Resolve multiple names
	ids, err := ResolveNamesToId(ctx, "domains", []string{"domain1", "domain2", "domain3"})

	assert.NilError(t, err)
	assert.Equal(t, len(ids), 3)
	assert.Equal(t, ids[0], "domain-id-1")
	assert.Equal(t, ids[1], "domain-id-2")
	assert.Equal(t, ids[2], "domain-id-3")
}

func TestResolveNameToId_NotFound(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	obj := object.New[object.Refrence]()
	obj.Set("name", "project")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	// Try to resolve non-indexed resource
	_, err := ResolveNameToId(ctx, "domains", "nonExistent")

	assert.ErrorContains(t, err, "not indexed")
}

func TestIndexById_EmptyContextPath(t *testing.T) {
	ctx := transform.NewContext[object.Refrence](context.Background())

	err := IndexById(ctx, "functions", "myFunction", "func-id-123")

	assert.ErrorContains(t, err, "context path is empty")
}

func TestIndexById_InvalidPathType(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, "invalid-type")

	err := IndexById(ctx, "functions", "myFunction", "func-id-123")

	assert.ErrorContains(t, err, "path contains invalid type")
}

func TestIndexById_ObjectWithoutName(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	// Create obj without name - this will be in ctp[1:]
	obj := object.New[object.Refrence]()
	// Don't set name on obj

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	err := IndexById(ctx, "functions", "myFunction", "func-id-123")

	assert.ErrorContains(t, err, "path contains no name")
}

func TestResolveNameToId_EmptyContextPath(t *testing.T) {
	ctx := transform.NewContext[object.Refrence](context.Background())

	_, err := ResolveNameToId(ctx, "domains", "myDomain")

	assert.ErrorContains(t, err, "context path is empty")
}

func TestResolveNamesToId_EmptyContextPath(t *testing.T) {
	ctx := transform.NewContext[object.Refrence](context.Background())

	_, err := ResolveNamesToId(ctx, "domains", []string{"domain1"})

	assert.ErrorContains(t, err, "context path is empty")
}

func TestResolveNamesToId_ErrorInResolve(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	obj := object.New[object.Refrence]()
	obj.Set("name", "project")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	// Try to resolve non-indexed resources
	_, err := ResolveNamesToId(ctx, "domains", []string{"nonExistent1", "nonExistent2"})

	assert.ErrorContains(t, err, "not indexed")
}

func TestLocalResolveNameToId_Success(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	obj := object.New[object.Refrence]()
	obj.Set("name", "project")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	// Index resource
	err := IndexById(ctx, "domains", "myDomain", "domain-id-456")
	assert.NilError(t, err)

	// Use localResolveNameToId directly
	ctp := ctx.Path()
	id, err := localResolveNameToId(ctx.Store(), ctp, "domains", "myDomain")
	assert.NilError(t, err)
	assert.Equal(t, id, "domain-id-456")
}

func TestLocalResolveNameToId_EmptyContextPath(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)

	// Empty path should return error
	_, err := localResolveNameToId(ctx.Store(), []any{}, "domains", "myDomain")
	assert.ErrorContains(t, err, "context path is empty")
}

func TestLocalResolveNameToId_NotFound(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	obj := object.New[object.Refrence]()
	obj.Set("name", "project")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	// Try to resolve non-indexed resource
	ctp := ctx.Path()
	_, err := localResolveNameToId(ctx.Store(), ctp, "domains", "nonExistent")
	assert.ErrorContains(t, err, "not indexed")
}

func TestLocalResolveNameToId_InvalidPathType(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)

	// Path with invalid type
	invalidPath := []any{root, "invalid-type"}
	_, err := localResolveNameToId(ctx.Store(), invalidPath, "domains", "myDomain")
	assert.ErrorContains(t, err, "path contains invalid type")
}

func TestLocalResolveNameToId_ObjectWithoutName(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	// Create obj without name
	obj := object.New[object.Refrence]()
	// Don't set name on obj

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	// Path contains object without name
	ctp := ctx.Path()
	_, err := localResolveNameToId(ctx.Store(), ctp, "domains", "myDomain")
	assert.ErrorContains(t, err, "path contains no name")
}
