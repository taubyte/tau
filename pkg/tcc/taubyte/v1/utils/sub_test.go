package utils

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestSub_ProcessInSubObject(t *testing.T) {
	// Setup: Create object with sub-object
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-123")

	// Execute: Use Sub wrapper to process in "object" sub-object
	wrapped := Sub(Global(transform.Transformer[object.Refrence](&mockTransformer{})), "object")
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := wrapped.Process(ctx, obj)

	// Verify: "object" child created
	assert.NilError(t, err)

	objectChild, err := result.Child("object").Object()
	assert.NilError(t, err)
	assert.Assert(t, objectChild != nil)

}

func TestSub_WithNestedPath(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-123")

	// Create functions in object
	funcsObj, _ := obj.CreatePath("object", "functions")
	funcSel := funcsObj.Child("myFunc")
	funcSel.Set("id", "func-id-456")
	funcSel.Set("type", "http")

	wrapped := Sub(Global(transform.Transformer[object.Refrence](&mockTransformer{})), "object")
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := wrapped.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	// Verify function still exists in sub-object (mock transformer doesn't rename, just sets processed flag)
	objectChild, _ := result.Child("object").Object()
	funcsObjAfter, _ := objectChild.Child("functions").Object()
	_, err = funcsObjAfter.Child("myFunc").Object()
	assert.NilError(t, err)

	// Verify processed flag was set
	processed := objectChild.Get("processed")
	assert.Equal(t, processed.(bool), true)

}

func TestSub_CreatesPathIfNotExists(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-123")

	wrapped := Sub(Global(transform.Transformer[object.Refrence](&mockTransformer{})), "newPath")
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := wrapped.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify path created
	_, err = result.Child("newPath").Object()
	assert.NilError(t, err)

}

func TestSub_ProcessError(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-123")

	wrapped := Sub(transform.Transformer[object.Refrence](&errorTransformer{}), "object")
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := wrapped.Process(ctx, obj)

	assert.ErrorContains(t, err, "processing sub-object failed")
}
