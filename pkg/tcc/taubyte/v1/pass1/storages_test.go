package pass1

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestStorages_WithSizeAndTTL(t *testing.T) {
	obj := object.New[object.Refrence]()
	storagesObj, _ := obj.CreatePath("storages")
	storageSel := storagesObj.Child("myStorage")
	storageSel.Set("id", "storage-id-123")
	storageSel.Set("size", "2GB")
	storageSel.Set("ttl", "1h")
	storageSel.Set("network-access", "all")

	transformer := Storages()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify storage renamed by ID
	renamedStorageSel := storagesObj.Child("storage-id-123")

	// Verify size converted to bytes (2GB = 2000000000 bytes in decimal)
	size, err := renamedStorageSel.Get("size")
	assert.NilError(t, err)
	assert.Equal(t, size.(int64), int64(2000000000))

	// Verify TTL converted to nanoseconds (1h = 3600000000000ns)
	ttl, err := renamedStorageSel.Get("ttl")
	assert.NilError(t, err)
	assert.Equal(t, ttl.(int64), int64(3600000000000))

	// Verify public set to true for "all" access
	public, err := renamedStorageSel.Get("public")
	assert.NilError(t, err)
	assert.Equal(t, public.(bool), true)

	// Verify network-access deleted (Get returns zero value, so we check it's not set)
	networkAccess, _ := renamedStorageSel.Get("network-access")
	assert.Assert(t, networkAccess == nil)

	// Verify name set
	name, err := renamedStorageSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "myStorage")

	// Verify indexed
	indexPath := "storages/myStorage"
	assert.Assert(t, ctx.Store().String(indexPath).Exist())
	assert.Equal(t, ctx.Store().String(indexPath).Get(), "storage-id-123")

}

func TestStorages_WithSubnetAccess(t *testing.T) {
	obj := object.New[object.Refrence]()
	storagesObj, _ := obj.CreatePath("storages")
	storageSel := storagesObj.Child("privateStorage")
	storageSel.Set("id", "storage-subnet-456")
	storageSel.Set("network-access", "subnet")

	transformer := Storages()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	renamedStorageSel := storagesObj.Child("storage-subnet-456")

	// Verify public set to false for "subnet" access
	public, err := renamedStorageSel.Get("public")
	assert.NilError(t, err)
	assert.Equal(t, public.(bool), false)
}

func TestStorages_NoStorages(t *testing.T) {
	obj := object.New[object.Refrence]()

	transformer := Storages()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestStorages_MultipleStorages(t *testing.T) {
	obj := object.New[object.Refrence]()
	storagesObj, _ := obj.CreatePath("storages")

	storage1 := storagesObj.Child("storage1")
	storage1.Set("id", "id1")
	storage1.Set("size", "1GB")

	storage2 := storagesObj.Child("storage2")
	storage2.Set("id", "id2")
	storage2.Set("ttl", "30m")

	transformer := Storages()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify both storages renamed
	_, err = storagesObj.Child("id1").Object()
	assert.NilError(t, err)

	_, err = storagesObj.Child("id2").Object()
	assert.NilError(t, err)

	// Verify both indexed
	assert.Assert(t, ctx.Store().String("storages/storage1").Exist())
	assert.Assert(t, ctx.Store().String("storages/storage2").Exist())
}
