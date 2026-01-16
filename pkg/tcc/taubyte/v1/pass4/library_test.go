package pass4

import (
	"context"
	"testing"

	specs "github.com/taubyte/tau/pkg/specs/library"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestLibraries_GlobalLibrary(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create library
	libraryConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	libSel := libraryConfig.Child("lib-id-456")
	libSel.Set("name", "myLibrary")
	libSel.Set("provider", "github")
	libSel.Set("repository-id", "123456")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Libraries("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	// Verify indexes created
	indexes, err := root.Child("indexes").Object()
	assert.NilError(t, err)
	assert.Assert(t, indexes != nil)

}

func TestLibraries_AppLibrary(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create app
	appsObj, _ := configRoot.CreatePath("applications")
	appObj := object.New[object.Refrence]()
	appSel := appsObj.Child("app-id-789")
	appSel.Add(appObj)

	// Create library in app
	libraryConfig, _ := appObj.CreatePath(string(specs.PathVariable))
	libSel := libraryConfig.Child("lib-id-999")
	libSel.Set("name", "appLibrary")
	libSel.Set("provider", "github")
	libSel.Set("repository-id", "789012")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(appObj)

	transformer := Libraries("main")
	result, err := transformer.Process(ctx, appObj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

}

func TestLibraries_NoLibraries(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Libraries("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestLibraries_WithExistingLinks(t *testing.T) {
	// Test case where links already contain the tnsPath
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create library
	libraryConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	libSel := libraryConfig.Child("lib-id-456")
	libSel.Set("name", "myLibrary")
	libSel.Set("provider", "github")
	libSel.Set("repository-id", "123456")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Libraries("main")

	// Process twice to test the Contains check
	_, err := transformer.Process(ctx, configRoot)
	assert.NilError(t, err)

	_, err = transformer.Process(ctx, configRoot)
	assert.NilError(t, err)
}
