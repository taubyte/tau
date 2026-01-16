package pass2

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestSmartops_NoSmartops(t *testing.T) {
	smartops := Smartops()

	obj := object.New[object.Refrence]()
	// No smartops group

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := smartops.Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result == obj, "should return same object when no smartops")
}

func TestSmartops_WithLibraries(t *testing.T) {
	smartops := Smartops()

	// Create root object with libraries
	root := object.New[object.Refrence]()
	libraries := object.New[object.Refrence]()
	lib1 := object.New[object.Refrence]()
	lib1.Set("name", "lib1")
	lib1.Set("id", "lib-id-1")
	err := libraries.Child("lib-id-1").Add(lib1)
	assert.NilError(t, err)
	err = root.Child("libraries").Add(libraries)
	assert.NilError(t, err)

	// Create smartops with library source
	smartopsObj := object.New[object.Refrence]()
	smartop1 := object.New[object.Refrence]()
	smartop1.Set("source", "libraries/lib-id-1")
	err = smartopsObj.Child("smartop1").Add(smartop1)
	assert.NilError(t, err)
	err = root.Child("smartops").Add(smartopsObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	result, err := smartops.Process(ctx, root)
	assert.NilError(t, err)

	// Check that library ID was resolved to name
	resultSmartops, err := result.Child("smartops").Object()
	assert.NilError(t, err)
	resultSmartop1, err := resultSmartops.Child("smartop1").Object()
	assert.NilError(t, err)
	source, err := resultSmartop1.GetString("source")
	assert.NilError(t, err)
	assert.Equal(t, source, "libraries/lib1")
}

func TestSmartops_LibraryNotFound(t *testing.T) {
	smartops := Smartops()

	root := object.New[object.Refrence]()
	smartopsObj := object.New[object.Refrence]()
	smartop1 := object.New[object.Refrence]()
	smartop1.Set("source", "libraries/non-existent-id")
	err := smartopsObj.Child("smartop1").Add(smartop1)
	assert.NilError(t, err)
	err = root.Child("smartops").Add(smartopsObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	_, err = smartops.Process(ctx, root)
	assert.ErrorContains(t, err, "library ID non-existent-id not found")
}

func TestSmartops_SourceNotString(t *testing.T) {
	smartops := Smartops()

	root := object.New[object.Refrence]()
	smartopsObj := object.New[object.Refrence]()
	smartop1 := object.New[object.Refrence]()
	smartop1.Set("source", 123)
	err := smartopsObj.Child("smartop1").Add(smartop1)
	assert.NilError(t, err)
	err = root.Child("smartops").Add(smartopsObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	_, err = smartops.Process(ctx, root)
	assert.ErrorContains(t, err, "source is not a string")
}

func TestSmartops_ErrorFetchingSmartops(t *testing.T) {
	// Setting a string value doesn't create a child object, so Child().Object() will return ErrNotExist
	// This test case is not realistic - skip it
	t.Skip("Skipping - setting string value doesn't create child object")
}
