package utils

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

func TestParseSize_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set size as string
	err := sel.Set("size", "2GB")
	assert.NilError(t, err)

	// Parse size
	err = ParseSize(sel, "size")
	assert.NilError(t, err)

	// Verify converted to bytes (2GB = 2000000000 bytes in decimal)
	size, err := sel.Get("size")
	assert.NilError(t, err)
	assert.Equal(t, size.(int64), int64(2000000000))
}

func TestParseSize_VariousSizes(t *testing.T) {
	testCases := []struct {
		name     string
		size     string
		expected int64
	}{
		{"500 MB", "500MB", 500000000},
		{"10 GB", "10GB", 10000000000},
		{"1 KB", "1KB", 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			obj := object.New[object.Refrence]()
			sel := obj.Child("test")
			sel.Set("size", tc.size)

			err := ParseSize(sel, "size")
			assert.NilError(t, err)

			size, err := sel.Get("size")
			assert.NilError(t, err)
			assert.Equal(t, size.(int64), tc.expected)
		})
	}
}

func TestParseSize_MissingField(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Field doesn't exist - should return no error
	err := ParseSize(sel, "nonexistent")
	assert.NilError(t, err)
}

func TestParseSize_NilField(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set field to nil explicitly
	err := sel.Set("size", nil)
	assert.NilError(t, err)

	// Should return no error
	err = ParseSize(sel, "size")
	assert.NilError(t, err)
}

func TestParseSize_InvalidType(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set size as int instead of string
	err := sel.Set("size", 123)
	assert.NilError(t, err)

	// Should return error
	err = ParseSize(sel, "size")
	assert.ErrorContains(t, err, "size is not a string")
}

func TestParseSize_InvalidSize(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set invalid size string
	err := sel.Set("size", "invalid-size")
	assert.NilError(t, err)

	// Should return error
	err = ParseSize(sel, "size")
	assert.ErrorContains(t, err, "parsing size failed")
}

func TestRenameById_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("originalName")

	// Set id
	err := sel.Set("id", "resource-id-123")
	assert.NilError(t, err)

	// Rename by id
	idStr, err := RenameById(sel, "newName")
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
	_, err := RenameById(sel, "name")
	assert.ErrorContains(t, err, "fetching id failed")
}

func TestRenameById_InvalidIdType(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set id as int instead of string
	err := sel.Set("id", 123)
	assert.NilError(t, err)

	// Should return error
	_, err = RenameById(sel, "name")
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
	_, err := RenameById(sel1, "name1")
	assert.NilError(t, err)

	// Try to rename second one to same id - should fail
	_, err = RenameById(sel2, "name2")
	assert.ErrorContains(t, err, "renaming to id failed")
}

func TestRenameById_CustomFieldName(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("originalName")

	// Set id with different value
	err := sel.Set("id", "custom-id-456")
	assert.NilError(t, err)

	// Rename by id with custom name
	idStr, err := RenameById(sel, "customName")
	assert.NilError(t, err)
	assert.Equal(t, idStr, "custom-id-456")

	// Verify name is set correctly
	renamedSel := obj.Child("custom-id-456")
	name, err := renamedSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "customName")
}

func TestFormatSize_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set size as bytes (2GB = 2000000000 bytes in decimal)
	err := sel.Set("size", int64(2000000000))
	assert.NilError(t, err)

	// Format size
	err = FormatSize(sel, "size")
	assert.NilError(t, err)

	// Verify converted to human-readable string
	size, err := sel.Get("size")
	assert.NilError(t, err)
	assert.Assert(t, size.(string) != "", "size should be formatted as string")
}

func TestFormatSize_MissingField(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Field doesn't exist - should return no error
	err := FormatSize(sel, "nonexistent")
	assert.NilError(t, err)
}

func TestFormatSize_InvalidType(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set size as string instead of integer
	err := sel.Set("size", "2GB")
	assert.NilError(t, err)

	// Should return error
	err = FormatSize(sel, "size")
	assert.ErrorContains(t, err, "size is not an integer")
}

func TestRenameByName_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("resource-id-123")

	// Set name
	err := sel.Set("name", "my-resource")
	assert.NilError(t, err)

	// Rename by name
	nameStr, err := RenameByName(sel)
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
	_, err := RenameByName(sel)
	assert.ErrorContains(t, err, "fetching name failed")
}

func TestRenameByName_InvalidNameType(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set name as int instead of string
	err := sel.Set("name", 123)
	assert.NilError(t, err)

	// Should return error
	_, err = RenameByName(sel)
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
	_, err = RenameByName(sel)
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
	_, err := RenameByName(sel1)
	assert.NilError(t, err)

	// Try to rename second one to same name - should fail
	_, err = RenameByName(sel2)
	assert.ErrorContains(t, err, "renaming to name failed")
}
