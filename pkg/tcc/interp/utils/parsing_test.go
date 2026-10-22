package utils

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

func TestRenameById_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("originalName")

	// Set id
	err := sel.Set("id", "resource-id-123")
	assert.NilError(t, err)

	// Rename by id
	idStr, err := RenameById(sel, "newName", "id", "name")
	assert.NilError(t, err)
	assert.Equal(t, idStr, "resource-id-123")

	// Verify renamed to id
	renamedSel := obj.Child("resource-id-123")
	exists := renamedSel.Exists()
	assert.Assert(t, exists)

	// Verify name is set
	name, err := renamedSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "newName")

	// Verify id is deleted (Get returns nil when field doesn't exist)
	idValue, err := renamedSel.Get("id")
	assert.NilError(t, err)
	assert.Assert(t, idValue == nil)
}

func TestRenameById_MissingId(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// No id field set

	// Should return error
	_, err := RenameById(sel, "name", "id", "name")
	assert.ErrorContains(t, err, "fetching id failed")
}

func TestRenameById_InvalidIdType(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set id as int instead of string
	err := sel.Set("id", 123)
	assert.NilError(t, err)

	// Should return error
	_, err = RenameById(sel, "name", "id", "name")
	assert.ErrorContains(t, err, "id is not a string")
}

func TestRenameById_DuplicateName(t *testing.T) {
	obj := object.New[object.Refrence]()

	// Create first child with id
	sel1 := obj.Child("first")
	sel1.Set("id", "id-1")

	// Create second child with same id (will cause rename conflict)
	sel2 := obj.Child("second")
	sel2.Set("id", "id-1")

	// Rename first one
	_, err := RenameById(sel1, "name1", "id", "name")
	assert.NilError(t, err)

	// Try to rename second one to same id - should fail
	_, err = RenameById(sel2, "name2", "id", "name")
	assert.ErrorContains(t, err, "renaming to id failed")
}

func TestRenameById_CustomFieldName(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("originalName")

	// Set id with different value
	err := sel.Set("id", "custom-id-456")
	assert.NilError(t, err)

	// Rename by id with custom name
	idStr, err := RenameById(sel, "customName", "id", "name")
	assert.NilError(t, err)
	assert.Equal(t, idStr, "custom-id-456")

	// Verify name is set correctly
	renamedSel := obj.Child("custom-id-456")
	name, err := renamedSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "customName")
}

func TestRenameByName_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("resource-id-123")

	// Set name
	err := sel.Set("name", "my-resource")
	assert.NilError(t, err)

	// Rename by name
	nameStr, err := RenameByName(sel, "id", "name")
	assert.NilError(t, err)
	assert.Equal(t, nameStr, "my-resource")

	// Verify renamed to name
	renamedSel := obj.Child("my-resource")
	exists := renamedSel.Exists()
	assert.Assert(t, exists)

	// Verify id is set to original key
	id, err := renamedSel.Get("id")
	assert.NilError(t, err)
	assert.Equal(t, id.(string), "resource-id-123")

	// Verify name is deleted
	nameValue, err := renamedSel.Get("name")
	assert.NilError(t, err)
	assert.Assert(t, nameValue == nil)
}

func TestRenameByName_MissingName(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// No name field set

	// Should return error
	_, err := RenameByName(sel, "id", "name")
	assert.ErrorContains(t, err, "fetching name failed")
}

func TestRenameByName_InvalidNameType(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set name as int instead of string
	err := sel.Set("name", 123)
	assert.NilError(t, err)

	// Should return error
	_, err = RenameByName(sel, "id", "name")
	assert.ErrorContains(t, err, "name is not a string")
}

func TestRenameByName_EmptyKey(t *testing.T) {
	// This is tricky - we need a selector with empty name
	// Let's test with a root object selector
	obj := object.New[object.Refrence]()
	sel := obj.Child("")

	// Set name
	err := sel.Set("name", "my-resource")
	assert.NilError(t, err)

	// Should return error because key is empty
	_, err = RenameByName(sel, "id", "name")
	assert.ErrorContains(t, err, "current key is empty")
}

func TestRenameByName_DuplicateName(t *testing.T) {
	obj := object.New[object.Refrence]()

	// Create first child with name
	sel1 := obj.Child("id-1")
	sel1.Set("name", "same-name")

	// Create second child with same name (will cause rename conflict)
	sel2 := obj.Child("id-2")
	sel2.Set("name", "same-name")

	// Rename first one
	_, err := RenameByName(sel1, "id", "name")
	assert.NilError(t, err)

	// Try to rename second one to same name - should fail
	_, err = RenameByName(sel2, "id", "name")
	assert.ErrorContains(t, err, "renaming to name failed")
}
