package pass3

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestDatabases_NoDatabases(t *testing.T) {
	databases := Databases()

	obj := object.New[object.Refrence]()
	// No databases group

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := databases.Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result == obj, "should return same object when no databases")
}

func TestDatabases_WithDatabases(t *testing.T) {
	databases := Databases()

	root := object.New[object.Refrence]()
	databasesObj := object.New[object.Refrence]()

	db1 := object.New[object.Refrence]()
	db1.Set("name", "my-database")
	db1.Set("id", "db-id-1")
	db1.Set("replicas-max", 5)
	db1.Set("replicas-min", 2)
	db1.Set("local", false)
	db1.Set("size", 10737418240) // 10GB in bytes (integer)
	err := databasesObj.Child("db-id-1").Add(db1)
	assert.NilError(t, err)

	err = root.Child("databases").Add(databasesObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := databases.Process(ctx, root)
	assert.NilError(t, err)

	// Check transformations
	resultDatabases, err := result.Child("databases").Object()
	assert.NilError(t, err)
	resultDb1, err := resultDatabases.Child("my-database").Object()
	assert.NilError(t, err)

	// Should have moved attributes (from max to replicas-max, etc.)
	replicasMax := resultDb1.Get("replicas-max")
	assert.Equal(t, replicasMax, 5)

	replicasMin := resultDb1.Get("replicas-min")
	assert.Equal(t, replicasMin, 2)

	// Should have converted local to network-access
	networkAccess, err := resultDb1.GetString("network-access")
	assert.NilError(t, err)
	assert.Equal(t, networkAccess, "all")

	// Local should be deleted
	_, err = resultDb1.GetBool("local")
	assert.ErrorContains(t, err, "not exist")
}

func TestDatabases_WithLocalTrue(t *testing.T) {
	databases := Databases()

	root := object.New[object.Refrence]()
	databasesObj := object.New[object.Refrence]()

	db1 := object.New[object.Refrence]()
	db1.Set("name", "local-db")
	db1.Set("id", "db-id-1")
	db1.Set("local", true)
	err := databasesObj.Child("db-id-1").Add(db1)
	assert.NilError(t, err)

	err = root.Child("databases").Add(databasesObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := databases.Process(ctx, root)
	assert.NilError(t, err)

	resultDatabases, err := result.Child("databases").Object()
	assert.NilError(t, err)
	resultDb1, err := resultDatabases.Child("local-db").Object()
	assert.NilError(t, err)

	// When local is true, network-access should be "host"
	networkAccess, err := resultDb1.GetString("network-access")
	assert.NilError(t, err)
	assert.Equal(t, networkAccess, "host")
}

func TestDatabases_ErrorFetchingDatabases(t *testing.T) {
	// Setting a string value doesn't create a child object, so Child().Object() will return ErrNotExist
	// This test case is not realistic - skip it
	t.Skip("Skipping - setting string value doesn't create child object")
}
