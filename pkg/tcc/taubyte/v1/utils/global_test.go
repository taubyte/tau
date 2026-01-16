package utils

import (
	"context"
	"fmt"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

// Mock transformer for testing
type mockTransformer struct{}

func (m *mockTransformer) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// Simple transformer that just sets a marker
	o.Set("processed", true)
	return o, nil
}

func TestGlobal_ProcessProjectAndApps(t *testing.T) {
	// Setup: Create project with applications
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-123")

	// Create global functions
	funcsObj, _ := obj.CreatePath("functions")
	globalFuncSel := funcsObj.Child("globalFunc")
	globalFuncSel.Set("id", "global-func-id")
	globalFuncSel.Set("type", "http")

	// Create applications with functions
	appsObj, _ := obj.CreatePath("applications")
	appObj := object.New[object.Refrence]()
	appSel := appsObj.Child("app-id-456")
	appSel.Add(appObj)
	appFuncsObj, _ := appObj.CreatePath("functions")
	appFuncSel := appFuncsObj.Child("appFunc")
	appFuncSel.Set("id", "app-func-id")
	appFuncSel.Set("type", "https")

	// Execute: Use Global wrapper with mock transformer
	wrapped := Global(transform.Transformer[object.Refrence](&mockTransformer{}))
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := wrapped.Process(ctx, obj)

	// Verify: Both global and app objects processed
	assert.NilError(t, err)

	// Verify global object processed (has processed marker)
	processed := result.Get("processed")
	assert.Equal(t, processed.(bool), true)

	// Verify app object processed
	appsObjAfter, _ := result.Child("applications").Object()
	appObjAfter, _ := appsObjAfter.Child("app-id-456").Object()
	appProcessed := appObjAfter.Get("processed")
	assert.Equal(t, appProcessed.(bool), true)

}

func TestGlobal_NoApplications(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-123")

	funcsObj, _ := obj.CreatePath("functions")
	funcSel := funcsObj.Child("myFunc")
	funcSel.Set("id", "func-id")
	funcSel.Set("type", "http")

	wrapped := Global(transform.Transformer[object.Refrence](&mockTransformer{}))
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := wrapped.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

}

func TestGlobal_MultipleApplications(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-123")

	// Create multiple applications
	appsObj, _ := obj.CreatePath("applications")

	app1Obj := object.New[object.Refrence]()
	app1Sel := appsObj.Child("app-id-1")
	app1Sel.Add(app1Obj)
	app1FuncsObj, _ := app1Obj.CreatePath("functions")
	app1FuncSel := app1FuncsObj.Child("func1")
	app1FuncSel.Set("id", "func-id-1")
	app1FuncSel.Set("type", "http")

	app2Obj := object.New[object.Refrence]()
	app2Sel := appsObj.Child("app-id-2")
	app2Sel.Add(app2Obj)
	app2FuncsObj, _ := app2Obj.CreatePath("functions")
	app2FuncSel := app2FuncsObj.Child("func2")
	app2FuncSel.Set("id", "func-id-2")
	app2FuncSel.Set("type", "https")

	wrapped := Global(transform.Transformer[object.Refrence](&mockTransformer{}))
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := wrapped.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

}

// Error transformer for testing error paths
type errorTransformer struct{}

func (m *errorTransformer) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	return nil, fmt.Errorf("transformer error")
}

func TestGlobal_ProcessError(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-123")

	wrapped := Global(transform.Transformer[object.Refrence](&errorTransformer{}))
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := wrapped.Process(ctx, obj)

	assert.ErrorContains(t, err, "processing global object failed")
}

// Transformer that succeeds on global but fails on applications
type appErrorTransformer struct{}

func (m *appErrorTransformer) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// Check if we're processing an application by checking the path length
	// After Fork(o), global has path length 2, applications have path length 3+
	if len(ct.Path()) > 2 {
		return nil, fmt.Errorf("application processing error")
	}
	// For global, just set a marker
	o.Set("processed", true)
	return o, nil
}

func TestGlobal_ApplicationProcessError(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-123")

	// Create application
	appsObj, _ := obj.CreatePath("applications")
	appObj := object.New[object.Refrence]()
	appSel := appsObj.Child("app-id-456")
	appSel.Add(appObj)

	// Use app error transformer
	wrapped := Global(transform.Transformer[object.Refrence](&appErrorTransformer{}))
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := wrapped.Process(ctx, obj)

	// Should fail when processing the application
	assert.ErrorContains(t, err, "processing application")
}
