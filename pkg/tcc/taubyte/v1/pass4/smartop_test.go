package pass4

import (
	"context"
	"testing"

	specs "github.com/taubyte/tau/pkg/specs/smartops"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestSmartops_GlobalSmartop(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create smartop
	smartopConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	smartopSel := smartopConfig.Child("smartop-id-456")
	smartopSel.Set("name", "mySmartop")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Smartops("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	// Verify indexes created
	indexes, err := root.Child("indexes").Object()
	assert.NilError(t, err)
	assert.Assert(t, indexes != nil)

}

func TestSmartops_AppSmartop(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create app
	appsObj, _ := configRoot.CreatePath("applications")
	appObj := object.New[object.Refrence]()
	appSel := appsObj.Child("app-id-789")
	appSel.Add(appObj)

	// Create smartop in app
	smartopConfig, _ := appObj.CreatePath(string(specs.PathVariable))
	smartopSel := smartopConfig.Child("smartop-id-999")
	smartopSel.Set("name", "appSmartop")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(appObj)

	transformer := Smartops("main")
	result, err := transformer.Process(ctx, appObj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

}

func TestSmartops_NoSmartops(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Smartops("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestSmartops_WithExistingLinks(t *testing.T) {
	// Test case where links already contain the tnsPath
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create smartop
	smartopConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	smartopSel := smartopConfig.Child("smartop-id-456")
	smartopSel.Set("name", "mySmartop")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Smartops("main")

	// Process twice to test the Contains check
	_, err := transformer.Process(ctx, configRoot)
	assert.NilError(t, err)

	_, err = transformer.Process(ctx, configRoot)
	assert.NilError(t, err)
}
