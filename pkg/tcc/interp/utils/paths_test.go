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

	err := IndexById(ctx, "functions", "myFunction", "func-id-123", "name")

	assert.NilError(t, err)

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

	err := IndexById(ctx, "domains", "myDomain", "domain-id-456", "name")
	assert.NilError(t, err)

	id, err := ResolveNameToId(ctx, "domains", "myDomain", "name")

	assert.NilError(t, err)
	assert.Equal(t, id, "domain-id-456")
}

func TestResolveNameToId_GlobalFallback(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	app := object.New[object.Refrence]()
	app.Set("name", "app")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	err := IndexById(ctx, "libraries", "myLib", "lib-id-789", "name")
	assert.NilError(t, err)

	indexPath := "libraries/myLib"
	assert.Assert(t, ctx.Store().String(indexPath).Exist())
	assert.Equal(t, ctx.Store().String(indexPath).Get(), "lib-id-789")

	ctxApp := ctx.Fork(app)

	// Resolve should fallback to parent scope (root)
	// First tries local: path [root, app], ctp[1:] is [app] -> "app/libraries/myLib" (doesn't exist)
	// Then falls back to ctp[:1] = [root] -> "libraries/myLib" (exists)
	id, err := ResolveNameToId(ctxApp, "libraries", "myLib", "name")

	assert.NilError(t, err)
	assert.Equal(t, id, "lib-id-789")
}

func TestResolveNameToId_ProjectScopeFallbackFromApp(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	project := object.New[object.Refrence]()
	project.Set("name", "myproject")

	app := object.New[object.Refrence]()
	app.Set("name", "myapp")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctxProject := ctx.Fork(project)

	err := IndexById(ctxProject, "libraries", "mylib", "lib-id-project", "name")
	assert.NilError(t, err)

	ctxApp := ctxProject.Fork(app)

	// Resolve from app context: first try app ("myproject/myapp/libraries/mylib") -> not found
	// then try project ("myproject/libraries/mylib") -> found
	id, err := ResolveNameToId(ctxApp, "libraries", "mylib", "name")
	assert.NilError(t, err)
	assert.Equal(t, id, "lib-id-project")
}

func TestResolveNameToId_ScopeQualifiers(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")
	app := object.New[object.Refrence]()
	app.Set("name", "app")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	// global library
	assert.NilError(t, IndexById(ctx, "libraries", "foo", "global-id", "name"))
	ctxApp := ctx.Fork(app)
	// app-local library, same name
	assert.NilError(t, IndexById(ctxApp, "libraries", "foo", "app-id", "name"))

	// default: current (app) scope wins
	id, err := ResolveNameToId(ctxApp, "libraries", "foo", "name")
	assert.NilError(t, err)
	assert.Equal(t, id, "app-id")

	// "/foo": absolute root/global, ignores the app-local one
	id, err = ResolveNameToId(ctxApp, "libraries", "/foo", "name")
	assert.NilError(t, err)
	assert.Equal(t, id, "global-id")

	// "../foo": the app's parent is root -> global
	id, err = ResolveNameToId(ctxApp, "libraries", "../foo", "name")
	assert.NilError(t, err)
	assert.Equal(t, id, "global-id")
}

func TestResolveNameToId_NoSiblingAppResolution(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")
	app1 := object.New[object.Refrence]()
	app1.Set("name", "app1")
	app2 := object.New[object.Refrence]()
	app2.Set("name", "app2")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	// library exists only in app1
	assert.NilError(t, IndexById(ctx.Fork(app1), "libraries", "foo", "app1-id", "name"))

	// from app2: unreachable, whether by default, absolute-root, or parent
	ctx2 := ctx.Fork(app2)
	for _, name := range []string{"foo", "/foo", "../foo"} {
		_, err := ResolveNameToId(ctx2, "libraries", name, "name")
		assert.ErrorContains(t, err, "not indexed", "sibling app resource must be unreachable: %q", name)
	}
}

func TestResolveNameToId_ScopeClimbsAboveRoot(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")
	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	assert.NilError(t, IndexById(ctx, "libraries", "foo", "gid", "name"))

	_, err := ResolveNameToId(ctx, "libraries", "../foo", "name")
	assert.ErrorContains(t, err, "climbs above the project root")
}

func TestResolveNamesToId_MultipleNames(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	obj := object.New[object.Refrence]()
	obj.Set("name", "project")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	IndexById(ctx, "domains", "domain1", "domain-id-1", "name")
	IndexById(ctx, "domains", "domain2", "domain-id-2", "name")
	IndexById(ctx, "domains", "domain3", "domain-id-3", "name")

	ids, err := ResolveNamesToId(ctx, "domains", []string{"domain1", "domain2", "domain3"}, "name")

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

	_, err := ResolveNameToId(ctx, "domains", "nonExistent", "name")

	assert.ErrorContains(t, err, "not indexed")
}

func TestIndexById_EmptyContextPath(t *testing.T) {
	ctx := transform.NewContext[object.Refrence](context.Background())

	err := IndexById(ctx, "functions", "myFunction", "func-id-123", "name")

	assert.ErrorContains(t, err, "context path is empty")
}

func TestIndexById_InvalidPathType(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, "invalid-type")

	err := IndexById(ctx, "functions", "myFunction", "func-id-123", "name")

	assert.ErrorContains(t, err, "path contains invalid type")
}

func TestIndexById_ObjectWithoutName(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	obj := object.New[object.Refrence]()

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	err := IndexById(ctx, "functions", "myFunction", "func-id-123", "name")

	assert.ErrorContains(t, err, "path contains no name")
}

func TestResolveNameToId_EmptyContextPath(t *testing.T) {
	ctx := transform.NewContext[object.Refrence](context.Background())

	_, err := ResolveNameToId(ctx, "domains", "myDomain", "name")

	assert.ErrorContains(t, err, "context path is empty")
}

func TestResolveNamesToId_EmptyContextPath(t *testing.T) {
	ctx := transform.NewContext[object.Refrence](context.Background())

	_, err := ResolveNamesToId(ctx, "domains", []string{"domain1"}, "name")

	assert.ErrorContains(t, err, "context path is empty")
}

func TestResolveNamesToId_ErrorInResolve(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	obj := object.New[object.Refrence]()
	obj.Set("name", "project")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	_, err := ResolveNamesToId(ctx, "domains", []string{"nonExistent1", "nonExistent2"}, "name")

	assert.ErrorContains(t, err, "not indexed")
}

func TestLocalResolveNameToId_Success(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	obj := object.New[object.Refrence]()
	obj.Set("name", "project")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	err := IndexById(ctx, "domains", "myDomain", "domain-id-456", "name")
	assert.NilError(t, err)

	ctp := ctx.Path()
	id, err := localResolveNameToId(ctx.Store(), ctp, "domains", "myDomain", "name")
	assert.NilError(t, err)
	assert.Equal(t, id, "domain-id-456")
}

func TestLocalResolveNameToId_EmptyContextPath(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)

	_, err := localResolveNameToId(ctx.Store(), []any{}, "domains", "myDomain", "name")
	assert.ErrorContains(t, err, "context path is empty")
}

func TestLocalResolveNameToId_NotFound(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	obj := object.New[object.Refrence]()
	obj.Set("name", "project")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	ctp := ctx.Path()
	_, err := localResolveNameToId(ctx.Store(), ctp, "domains", "nonExistent", "name")
	assert.ErrorContains(t, err, "not indexed")
}

func TestLocalResolveNameToId_InvalidPathType(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	ctx := transform.NewContext[object.Refrence](context.Background(), root)

	invalidPath := []any{root, "invalid-type"}
	_, err := localResolveNameToId(ctx.Store(), invalidPath, "domains", "myDomain", "name")
	assert.ErrorContains(t, err, "path contains invalid type")
}

func TestLocalResolveNameToId_ObjectWithoutName(t *testing.T) {
	root := object.New[object.Refrence]()
	root.Set("name", "root")

	obj := object.New[object.Refrence]()

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	ctx = ctx.Fork(obj)

	ctp := ctx.Path()
	_, err := localResolveNameToId(ctx.Store(), ctp, "domains", "myDomain", "name")
	assert.ErrorContains(t, err, "path contains no name")
}
