package interp

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestChroot_WrapsInObject(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-123")
	funcsObj, _ := obj.CreatePath("functions")
	funcSel := funcsObj.Child("func-id-456")
	funcSel.Set("name", "myFunction")

	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := (&chroot{}).Process(ctx, obj)
	assert.NilError(t, err)

	objectChild, err := result.Child("object").Object()
	assert.NilError(t, err)

	id := objectChild.Get("id")
	assert.Equal(t, id.(string), "project-id-123")

	_, err = objectChild.Child("functions").Object()
	assert.NilError(t, err)
}

func TestChroot_EmptyObject(t *testing.T) {
	obj := object.New[object.Refrence]()

	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := (&chroot{}).Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	_, err = result.Child("object").Object()
	assert.NilError(t, err)
}

func TestChroot_WithNestedResources(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id")

	funcsObj, _ := obj.CreatePath("functions")
	funcsObj.Child("func1").Set("id", "id1")

	domainsObj, _ := obj.CreatePath("domains")
	domainsObj.Child("domain1").Set("id", "id2")

	websitesObj, _ := obj.CreatePath("websites")
	websitesObj.Child("website1").Set("id", "id3")

	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := (&chroot{}).Process(ctx, obj)
	assert.NilError(t, err)

	objectChild, err := result.Child("object").Object()
	assert.NilError(t, err)

	_, err = objectChild.Child("functions").Object()
	assert.NilError(t, err)

	_, err = objectChild.Child("domains").Object()
	assert.NilError(t, err)

	_, err = objectChild.Child("websites").Object()
	assert.NilError(t, err)
}

func TestChrootEnvelope_WrapsProject(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := chrootEnvelope().Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	_, err = result.Child("object").Object()
	assert.NilError(t, err)
}
