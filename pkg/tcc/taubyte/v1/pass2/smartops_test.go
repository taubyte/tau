package pass2

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestSmartops_ResolveLibrarySource(t *testing.T) {
	obj := object.New[object.Refrence]()

	// Create libraries first
	librariesObj, _ := obj.CreatePath("libraries")
	libSel := librariesObj.Child("mylib")
	libSel.Set("id", "lib-id-123")

	// Create smartop with library source name
	smartopsObj, _ := obj.CreatePath("smartops")
	smartopSel := smartopsObj.Child("smartop-id-456")
	smartopSel.Set("source", "libraries/mylib")

	// Index library
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	err := utils.IndexById(ctx, "libraries", "mylib", "lib-id-123")
	assert.NilError(t, err)

	// Execute: Run pass2 transformer
	transformer := Smartops()
	_, err = transformer.Process(ctx, obj)

	// Verify: Library name resolved to ID
	assert.NilError(t, err)

	source, err := smartopsObj.Child("smartop-id-456").Get("source")
	assert.NilError(t, err)
	assert.Equal(t, source.(string), "libraries/lib-id-123")

}

func TestSmartops_NoSmartops(t *testing.T) {
	obj := object.New[object.Refrence]()

	transformer := Smartops()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestSmartops_NoLibrarySource(t *testing.T) {
	obj := object.New[object.Refrence]()

	smartopsObj, _ := obj.CreatePath("smartops")
	smartopSel := smartopsObj.Child("smartop-id-789")
	smartopSel.Set("timeout", "10s")
	// No source field

	transformer := Smartops()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestSmartops_MultipleSmartops(t *testing.T) {
	obj := object.New[object.Refrence]()

	// Setup libraries
	librariesObj, _ := obj.CreatePath("libraries")
	lib1Sel := librariesObj.Child("lib1")
	lib1Sel.Set("id", "lib-id-1")
	lib2Sel := librariesObj.Child("lib2")
	lib2Sel.Set("id", "lib-id-2")

	// Setup smartops
	smartopsObj, _ := obj.CreatePath("smartops")
	smartop1Sel := smartopsObj.Child("smartop-id-1")
	smartop1Sel.Set("source", "libraries/lib1")
	smartop2Sel := smartopsObj.Child("smartop-id-2")
	smartop2Sel.Set("source", "libraries/lib2")

	// Index libraries
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	utils.IndexById(ctx, "libraries", "lib1", "lib-id-1")
	utils.IndexById(ctx, "libraries", "lib2", "lib-id-2")

	// Execute
	transformer := Smartops()
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify both resolved
	source1, _ := smartopsObj.Child("smartop-id-1").Get("source")
	assert.Equal(t, source1.(string), "libraries/lib-id-1")

	source2, _ := smartopsObj.Child("smartop-id-2").Get("source")
	assert.Equal(t, source2.(string), "libraries/lib-id-2")
}
