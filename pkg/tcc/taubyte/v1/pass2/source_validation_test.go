package pass2

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func runSourceValidation(t *testing.T, obj object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	transformer := utils.Global(SourceValidation())
	return transformer.Process(ctx, obj)
}

func TestSourceValidation_ValidInline(t *testing.T) {
	obj := object.New[object.Refrence]()
	funcsObj, _ := obj.CreatePath("functions")
	funcSel := funcsObj.Child("func-1")
	funcSel.Set("source", ".")

	_, err := runSourceValidation(t, obj)
	assert.NilError(t, err)
}

func TestSourceValidation_ValidLibrariesPrefix(t *testing.T) {
	obj := object.New[object.Refrence]()
	funcsObj, _ := obj.CreatePath("functions")
	funcSel := funcsObj.Child("func-1")
	funcSel.Set("source", "libraries/mylib")

	_, err := runSourceValidation(t, obj)
	assert.NilError(t, err)
}

func TestSourceValidation_InvalidLibrarySingular(t *testing.T) {
	obj := object.New[object.Refrence]()
	funcsObj, _ := obj.CreatePath("functions")
	funcSel := funcsObj.Child("func-1")
	funcSel.Set("source", "library/mylib")

	_, err := runSourceValidation(t, obj)
	assert.ErrorContains(t, err, "source must be")
	assert.ErrorContains(t, err, "library/mylib")
}

func TestSourceValidation_InvalidFoo(t *testing.T) {
	obj := object.New[object.Refrence]()
	funcsObj, _ := obj.CreatePath("functions")
	funcSel := funcsObj.Child("func-1")
	funcSel.Set("source", "foo")

	_, err := runSourceValidation(t, obj)
	assert.ErrorContains(t, err, "source must be")
	assert.ErrorContains(t, err, "foo")
}

func TestSourceValidation_MissingSource(t *testing.T) {
	obj := object.New[object.Refrence]()
	funcsObj, _ := obj.CreatePath("functions")
	funcsObj.Child("func-1")
	// no source set

	_, err := runSourceValidation(t, obj)
	assert.NilError(t, err)
}

func TestSourceValidation_SmartopValidInline(t *testing.T) {
	obj := object.New[object.Refrence]()
	smartopsObj, _ := obj.CreatePath("smartops")
	sel := smartopsObj.Child("smartop-1")
	sel.Set("source", ".")

	_, err := runSourceValidation(t, obj)
	assert.NilError(t, err)
}

func TestSourceValidation_SmartopInvalid(t *testing.T) {
	obj := object.New[object.Refrence]()
	smartopsObj, _ := obj.CreatePath("smartops")
	sel := smartopsObj.Child("smartop-1")
	sel.Set("source", "other")

	_, err := runSourceValidation(t, obj)
	assert.ErrorContains(t, err, "smartop")
	assert.ErrorContains(t, err, "source must be")
}

func TestSourceValidation_NoFunctionsOrSmartops(t *testing.T) {
	obj := object.New[object.Refrence]()

	_, err := runSourceValidation(t, obj)
	assert.NilError(t, err)
}

func TestSourceValidation_AppLevelValidation(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("name", "project")
	appsObj, _ := obj.CreatePath("applications")
	appObj := object.New[object.Refrence]()
	appObj.Set("name", "app1")
	funcsObj, _ := appObj.CreatePath("functions")
	funcSel := funcsObj.Child("func-app")
	funcSel.Set("source", ".")
	appSel := appsObj.Child("app-1")
	appSel.Add(appObj)

	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	transformer := utils.Global(SourceValidation())
	_, err := transformer.Process(ctx, obj)
	assert.NilError(t, err)
}
