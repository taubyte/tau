package pass4

import (
	"context"
	"testing"

	specs "github.com/taubyte/tau/pkg/specs/database"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestDatabase_GlobalDatabase(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create database
	databaseConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	dbSel := databaseConfig.Child("db-id-456")
	dbSel.Set("name", "myDatabase")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Database("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	// Verify indexes created
	indexes, err := root.Child("indexes").Object()
	assert.NilError(t, err)
	assert.Assert(t, indexes != nil)

}

func TestDatabase_AppDatabase(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create app
	appsObj, _ := configRoot.CreatePath("applications")
	appObj := object.New[object.Refrence]()
	appSel := appsObj.Child("app-id-789")
	appSel.Add(appObj)

	// Create database in app
	databaseConfig, _ := appObj.CreatePath(string(specs.PathVariable))
	dbSel := databaseConfig.Child("db-id-999")
	dbSel.Set("name", "appDatabase")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(appObj)

	transformer := Database("main")
	result, err := transformer.Process(ctx, appObj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

}

func TestDatabase_NoDatabases(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Database("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestDatabase_WithExistingLinks(t *testing.T) {
	// Test case where links already contain the tnsPath
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create database
	databaseConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	dbSel := databaseConfig.Child("db-id-456")
	dbSel.Set("name", "myDatabase")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Database("main")

	// Process twice to test the Contains check
	_, err := transformer.Process(ctx, configRoot)
	assert.NilError(t, err)

	_, err = transformer.Process(ctx, configRoot)
	assert.NilError(t, err)
}
