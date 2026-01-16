package pass1

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestDatabases_WithSizeAndAccess(t *testing.T) {
	obj := object.New[object.Refrence]()
	databasesObj, _ := obj.CreatePath("databases")
	dbSel := databasesObj.Child("myDatabase")
	dbSel.Set("id", "db-id-123")
	dbSel.Set("size", "1GB")
	dbSel.Set("network-access", "all")
	dbSel.Set("replicas-max", "5")
	dbSel.Set("replicas-min", "2")

	transformer := Databases()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify database renamed by ID
	renamedDbSel := databasesObj.Child("db-id-123")

	// Verify size converted to bytes (1GB = 1000000000 bytes in decimal)
	size, err := renamedDbSel.Get("size")
	assert.NilError(t, err)
	assert.Equal(t, size.(int64), int64(1000000000))

	// Verify local set to false for "all" access
	local, err := renamedDbSel.Get("local")
	assert.NilError(t, err)
	assert.Equal(t, local.(bool), false)

	// Verify attributes moved
	max, err := renamedDbSel.Get("max")
	assert.NilError(t, err)
	assert.Equal(t, max.(string), "5")

	min, err := renamedDbSel.Get("min")
	assert.NilError(t, err)
	assert.Equal(t, min.(string), "2")

	// Verify name set
	name, err := renamedDbSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "myDatabase")

	// Verify indexed
	indexPath := "databases/myDatabase"
	assert.Assert(t, ctx.Store().String(indexPath).Exist())
	assert.Equal(t, ctx.Store().String(indexPath).Get(), "db-id-123")

}

func TestDatabases_WithHostAccess(t *testing.T) {
	obj := object.New[object.Refrence]()
	databasesObj, _ := obj.CreatePath("databases")
	dbSel := databasesObj.Child("localDatabase")
	dbSel.Set("id", "db-host-456")
	dbSel.Set("network-access", "host")
	dbSel.Set("size", "1GB") // Add size to avoid nil access

	transformer := Databases()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	renamedDbSel := databasesObj.Child("db-host-456")

	// Verify local set to true for "host" access
	local, err := renamedDbSel.Get("local")
	assert.NilError(t, err)
	assert.Equal(t, local.(bool), true)
}

func TestDatabases_NoDatabases(t *testing.T) {
	obj := object.New[object.Refrence]()

	transformer := Databases()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestDatabases_MultipleDatabases(t *testing.T) {
	obj := object.New[object.Refrence]()
	databasesObj, _ := obj.CreatePath("databases")

	db1 := databasesObj.Child("database1")
	db1.Set("id", "id1")
	db1.Set("size", "500MB")

	db2 := databasesObj.Child("database2")
	db2.Set("id", "id2")
	db2.Set("network-access", "host")
	db2.Set("size", "500MB") // Add size to avoid nil access

	transformer := Databases()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify both databases renamed
	_, err = databasesObj.Child("id1").Object()
	assert.NilError(t, err)

	_, err = databasesObj.Child("id2").Object()
	assert.NilError(t, err)

	// Verify both indexed
	assert.Assert(t, ctx.Store().String("databases/database1").Exist())
	assert.Assert(t, ctx.Store().String("databases/database2").Exist())
}
