package pass4

import (
	"context"
	"testing"

	specs "github.com/taubyte/tau/pkg/specs/storage"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestStorage_GlobalStorage(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create storage
	storageConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	storageSel := storageConfig.Child("storage-id-456")
	storageSel.Set("name", "myStorage")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Storage("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	// Verify indexes created
	indexes, err := root.Child("indexes").Object()
	assert.NilError(t, err)
	assert.Assert(t, indexes != nil)

}

func TestStorage_AppStorage(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create app
	appsObj, _ := configRoot.CreatePath("applications")
	appObj := object.New[object.Refrence]()
	appSel := appsObj.Child("app-id-789")
	appSel.Add(appObj)

	// Create storage in app
	storageConfig, _ := appObj.CreatePath(string(specs.PathVariable))
	storageSel := storageConfig.Child("storage-id-999")
	storageSel.Set("name", "appStorage")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(appObj)

	transformer := Storage("main")
	result, err := transformer.Process(ctx, appObj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

}

func TestStorage_NoStorages(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Storage("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestStorage_WithExistingLinks(t *testing.T) {
	// Test case where links already contain the tnsPath
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create storage
	storageConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	storageSel := storageConfig.Child("storage-id-456")
	storageSel.Set("name", "myStorage")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Storage("main")

	// Process twice to test the Contains check
	_, err := transformer.Process(ctx, configRoot)
	assert.NilError(t, err)

	_, err = transformer.Process(ctx, configRoot)
	assert.NilError(t, err)
}
