package pass3

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestStorages_NoStorages(t *testing.T) {
	storages := Storages()

	obj := object.New[object.Refrence]()
	// No storages group

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := storages.Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result == obj, "should return same object when no storages")
}

func TestStorages_WithStorages(t *testing.T) {
	storages := Storages()

	root := object.New[object.Refrence]()
	storagesObj := object.New[object.Refrence]()

	storage1 := object.New[object.Refrence]()
	storage1.Set("name", "my-storage")
	storage1.Set("id", "storage-id-1")
	storage1.Set("public", false)
	storage1.Set("size", 10737418240) // 10GB in bytes (integer)
	storage1.Set("ttl", 3600)         // 1 hour in seconds (integer)
	err := storagesObj.Child("storage-id-1").Add(storage1)
	assert.NilError(t, err)

	err = root.Child("storages").Add(storagesObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := storages.Process(ctx, root)
	assert.NilError(t, err)

	// Check transformations
	resultStorages, err := result.Child("storages").Object()
	assert.NilError(t, err)
	resultStorage1, err := resultStorages.Child("my-storage").Object()
	assert.NilError(t, err)

	// Should have converted public to network-access
	networkAccess, err := resultStorage1.GetString("network-access")
	assert.NilError(t, err)
	assert.Equal(t, networkAccess, "subnet")

	// Public should be deleted
	_, err = resultStorage1.GetBool("public")
	assert.ErrorContains(t, err, "not exist")
}

func TestStorages_WithPublicTrue(t *testing.T) {
	storages := Storages()

	root := object.New[object.Refrence]()
	storagesObj := object.New[object.Refrence]()

	storage1 := object.New[object.Refrence]()
	storage1.Set("name", "public-storage")
	storage1.Set("id", "storage-id-1")
	storage1.Set("public", true)
	err := storagesObj.Child("storage-id-1").Add(storage1)
	assert.NilError(t, err)

	err = root.Child("storages").Add(storagesObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := storages.Process(ctx, root)
	assert.NilError(t, err)

	resultStorages, err := result.Child("storages").Object()
	assert.NilError(t, err)
	resultStorage1, err := resultStorages.Child("public-storage").Object()
	assert.NilError(t, err)

	// When public is true, network-access should be "all"
	networkAccess, err := resultStorage1.GetString("network-access")
	assert.NilError(t, err)
	assert.Equal(t, networkAccess, "all")
}
