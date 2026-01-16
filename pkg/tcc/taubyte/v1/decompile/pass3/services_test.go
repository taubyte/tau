package pass3

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestServices_NoServices(t *testing.T) {
	services := Services()

	obj := object.New[object.Refrence]()
	// No services group

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := services.Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result == obj, "should return same object when no services")
}

func TestServices_WithServices(t *testing.T) {
	services := Services()

	root := object.New[object.Refrence]()
	servicesObj := object.New[object.Refrence]()

	svc1 := object.New[object.Refrence]()
	svc1.Set("name", "my-service")
	svc1.Set("id", "svc-id-1")
	err := servicesObj.Child("svc-id-1").Add(svc1)
	assert.NilError(t, err)

	err = root.Child("services").Add(servicesObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := services.Process(ctx, root)
	assert.NilError(t, err)

	// Check that service was renamed by name
	resultServices, err := result.Child("services").Object()
	assert.NilError(t, err)
	resultSvc1, err := resultServices.Child("my-service").Object()
	assert.NilError(t, err)

	id, err := resultSvc1.GetString("id")
	assert.NilError(t, err)
	assert.Equal(t, id, "svc-id-1")
}

func TestServices_MissingName(t *testing.T) {
	services := Services()

	root := object.New[object.Refrence]()
	servicesObj := object.New[object.Refrence]()

	svc1 := object.New[object.Refrence]()
	svc1.Set("id", "svc-id-1")
	// Missing name
	err := servicesObj.Child("svc-id-1").Add(svc1)
	assert.NilError(t, err)

	err = root.Child("services").Add(servicesObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	_, err = services.Process(ctx, root)
	assert.ErrorContains(t, err, "fetching name for service")
}
