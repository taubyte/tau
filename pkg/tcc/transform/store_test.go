package transform

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

func TestStore_StringOperations(t *testing.T) {
	ctx := NewContext[object.Refrence](context.Background())
	store := ctx.Store()

	// Test Set and Get
	_, err := store.String("key1").Set("value1")
	assert.NilError(t, err)

	assert.Assert(t, store.String("key1").Exist())
	assert.Equal(t, store.String("key1").Get(), "value1")

	// Test overwrite
	_, err = store.String("key1").Set("value2")
	assert.NilError(t, err)
	assert.Equal(t, store.String("key1").Get(), "value2")

	// Test Delete
	err = store.String("key1").Del()
	assert.NilError(t, err)
	assert.Assert(t, !store.String("key1").Exist())
}

func TestStore_BytesOperations(t *testing.T) {
	ctx := NewContext[object.Refrence](context.Background())
	store := ctx.Store()

	data := []byte{1, 2, 3, 4, 5}

	// Test Set and Get
	_, err := store.Bytes("bytesKey").Set(data)
	assert.NilError(t, err)

	assert.Assert(t, store.Bytes("bytesKey").Exist())
	retrieved := store.Bytes("bytesKey").Get()
	assert.DeepEqual(t, retrieved, data)

	// Test Delete
	err = store.Bytes("bytesKey").Del()
	assert.NilError(t, err)
	assert.Assert(t, !store.Bytes("bytesKey").Exist())
}

func TestStore_ObjectOperations(t *testing.T) {
	ctx := NewContext[object.Refrence](context.Background())
	store := ctx.Store()

	obj := object.New[object.Refrence]()
	obj.Set("test", "value")

	// Test Set and Get
	_, err := store.Object("objKey").Set(obj)
	assert.NilError(t, err)

	assert.Assert(t, store.Object("objKey").Exist())
	retrieved := store.Object("objKey").Get()
	assert.Assert(t, retrieved != nil)
	assert.Equal(t, retrieved.Get("test"), "value")

	// Test Delete
	err = store.Object("objKey").Del()
	assert.NilError(t, err)
	assert.Assert(t, !store.Object("objKey").Exist())
}

func TestStore_MultipleKeys(t *testing.T) {
	ctx := NewContext[object.Refrence](context.Background())
	store := ctx.Store()

	// Set multiple string keys
	store.String("key1").Set("value1")
	store.String("key2").Set("value2")
	store.String("key3").Set("value3")

	// Verify all exist
	assert.Assert(t, store.String("key1").Exist())
	assert.Assert(t, store.String("key2").Exist())
	assert.Assert(t, store.String("key3").Exist())

	// Verify values
	assert.Equal(t, store.String("key1").Get(), "value1")
	assert.Equal(t, store.String("key2").Get(), "value2")
	assert.Equal(t, store.String("key3").Get(), "value3")
}

func TestStore_NonExistentKey(t *testing.T) {
	ctx := NewContext[object.Refrence](context.Background())
	store := ctx.Store()

	// Test non-existent key
	assert.Assert(t, !store.String("nonExistent").Exist())

	// Get should return zero value
	value := store.String("nonExistent").Get()
	assert.Equal(t, value, "")
}

func TestStore_ValidatorsOperations(t *testing.T) {
	ctx := NewContext[object.Refrence](context.Background())
	store := ctx.Store()

	// Initially should be empty
	validators := store.Validators()
	assert.Assert(t, !validators.Exist())
	retrieved := validators.Get()
	assert.Assert(t, retrieved != nil)
	assert.Equal(t, len(retrieved), 0)

	// Create test validations
	validation1 := engine.NewNextValidation(
		"domain",
		"example.com",
		"dns",
		map[string]interface{}{
			"project": "proj-123",
		},
	)

	validation2 := engine.NewNextValidation(
		"domain",
		"test.example.com",
		"dns",
		map[string]interface{}{
			"project": "proj-123",
			"app":     "app-456",
		},
	)

	validations := []engine.NextValidation{validation1, validation2}

	// Test Set
	_, err := validators.Set(validations)
	assert.NilError(t, err)

	// Test Exist
	assert.Assert(t, validators.Exist())

	// Test Get
	retrieved = validators.Get()
	assert.Equal(t, len(retrieved), 2)
	assert.Equal(t, retrieved[0].Key, "domain")
	assert.Equal(t, retrieved[0].Value, "example.com")
	assert.Equal(t, retrieved[0].Validator, "dns")
	assert.Equal(t, retrieved[0].Context["project"], "proj-123")

	assert.Equal(t, retrieved[1].Key, "domain")
	assert.Equal(t, retrieved[1].Value, "test.example.com")
	assert.Equal(t, retrieved[1].Validator, "dns")
	assert.Equal(t, retrieved[1].Context["project"], "proj-123")
	assert.Equal(t, retrieved[1].Context["app"], "app-456")

	// Test overwrite
	newValidation := []engine.NextValidation{
		engine.NewNextValidation("test", "value", "validator", map[string]interface{}{}),
	}
	_, err = validators.Set(newValidation)
	assert.NilError(t, err)
	retrieved = validators.Get()
	assert.Equal(t, len(retrieved), 1)
	assert.Equal(t, retrieved[0].Key, "test")

	// Test Delete
	err = validators.Del()
	assert.NilError(t, err)
	assert.Assert(t, !validators.Exist())
	retrieved = validators.Get()
	assert.Equal(t, len(retrieved), 0)
}

func TestStore_ValidatorsAppend(t *testing.T) {
	ctx := NewContext[object.Refrence](context.Background())
	store := ctx.Store()

	validators := store.Validators()

	// Add first validation
	validation1 := engine.NewNextValidation("domain", "example.com", "dns", map[string]interface{}{"project": "proj-1"})
	_, err := validators.Set([]engine.NextValidation{validation1})
	assert.NilError(t, err)

	// Get, append, and set back
	existing := validators.Get()
	validation2 := engine.NewNextValidation("domain", "test.com", "dns", map[string]interface{}{"project": "proj-2"})
	existing = append(existing, validation2)
	_, err = validators.Set(existing)
	assert.NilError(t, err)

	// Verify both are present
	retrieved := validators.Get()
	assert.Equal(t, len(retrieved), 2)
	assert.Equal(t, retrieved[0].Value, "example.com")
	assert.Equal(t, retrieved[1].Value, "test.com")
}
