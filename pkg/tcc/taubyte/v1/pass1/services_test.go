package pass1

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestServices_WithProtocol(t *testing.T) {
	obj := object.New[object.Refrence]()
	servicesObj, _ := obj.CreatePath("services")
	serviceSel := servicesObj.Child("myService")
	serviceSel.Set("id", "service-id-123")
	serviceSel.Set("protocol", "p2p")

	transformer := Services()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify service renamed by ID
	renamedServiceSel := servicesObj.Child("service-id-123")

	// Verify name set
	name, err := renamedServiceSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "myService")

	// Verify indexed
	indexPath := "services/myService"
	assert.Assert(t, ctx.Store().String(indexPath).Exist())
	assert.Equal(t, ctx.Store().String(indexPath).Get(), "service-id-123")

}

func TestServices_NoServices(t *testing.T) {
	obj := object.New[object.Refrence]()

	transformer := Services()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestServices_MultipleServices(t *testing.T) {
	obj := object.New[object.Refrence]()
	servicesObj, _ := obj.CreatePath("services")

	service1 := servicesObj.Child("service1")
	service1.Set("id", "id1")
	service1.Set("protocol", "p2p")

	service2 := servicesObj.Child("service2")
	service2.Set("id", "id2")
	service2.Set("protocol", "http")

	transformer := Services()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify both services renamed
	_, err = servicesObj.Child("id1").Object()
	assert.NilError(t, err)

	_, err = servicesObj.Child("id2").Object()
	assert.NilError(t, err)

	// Verify both indexed
	assert.Assert(t, ctx.Store().String("services/service1").Exist())
	assert.Assert(t, ctx.Store().String("services/service2").Exist())
}
