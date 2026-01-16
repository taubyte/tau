package pass1

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestApplications_WithNestedResources(t *testing.T) {
	obj := object.New[object.Refrence]()
	appsObj, _ := obj.CreatePath("applications")
	appSel := appsObj.Child("myApp")
	appSel.Set("id", "app-id-123")

	// Add nested functions
	appObj, _ := appSel.Object()
	funcsObj, _ := appObj.CreatePath("functions")
	funcSel := funcsObj.Child("appFunction")
	funcSel.Set("id", "func-id-456")

	transformer := Applications()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify application renamed by ID
	renamedAppSel := appsObj.Child("app-id-123")

	// Verify name set
	name, err := renamedAppSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "myApp")

}

func TestApplications_NoApplications(t *testing.T) {
	obj := object.New[object.Refrence]()

	transformer := Applications()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestApplications_MultipleApplications(t *testing.T) {
	obj := object.New[object.Refrence]()
	appsObj, _ := obj.CreatePath("applications")

	app1 := appsObj.Child("app1")
	app1.Set("id", "id1")

	app2 := appsObj.Child("app2")
	app2.Set("id", "id2")

	transformer := Applications()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify both applications renamed
	_, err = appsObj.Child("id1").Object()
	assert.NilError(t, err)

	_, err = appsObj.Child("id2").Object()
	assert.NilError(t, err)
}
