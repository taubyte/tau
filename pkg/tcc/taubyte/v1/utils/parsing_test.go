package utils

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

func TestParseTimeout_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set timeout as string
	err := sel.Set("timeout", "5s")
	assert.NilError(t, err)

	// Parse timeout
	err = ParseTimeout(sel, "timeout")
	assert.NilError(t, err)

	// Verify converted to nanoseconds (5s = 5000000000ns)
	timeout, err := sel.Get("timeout")
	assert.NilError(t, err)
	assert.Equal(t, timeout.(int64), int64(5000000000))
}

func TestParseTimeout_VariousDurations(t *testing.T) {
	testCases := []struct {
		name     string
		duration string
		expected int64
	}{
		{"1 hour", "1h", 3600000000000},
		{"30 minutes", "30m", 1800000000000},
		{"2 seconds", "2s", 2000000000},
		{"500 milliseconds", "500ms", 500000000},
		{"1 hour 30 minutes", "1h30m", 5400000000000},
		{"1.5 seconds", "1.5s", 1500000000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			obj := object.New[object.Refrence]()
			sel := obj.Child("test")
			sel.Set("timeout", tc.duration)

			err := ParseTimeout(sel, "timeout")
			assert.NilError(t, err)

			timeout, err := sel.Get("timeout")
			assert.NilError(t, err)
			assert.Equal(t, timeout.(int64), tc.expected)
		})
	}
}

func TestParseTimeout_MissingField(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Field doesn't exist - should return no error
	err := ParseTimeout(sel, "nonexistent")
	assert.NilError(t, err)
}

func TestParseTimeout_NilField(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set field to nil explicitly
	err := sel.Set("timeout", nil)
	assert.NilError(t, err)

	// Should return no error
	err = ParseTimeout(sel, "timeout")
	assert.NilError(t, err)
}

func TestParseTimeout_InvalidType(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set timeout as int instead of string
	err := sel.Set("timeout", 123)
	assert.NilError(t, err)

	// Should return error
	err = ParseTimeout(sel, "timeout")
	assert.ErrorContains(t, err, "timeout is not a string")
}

func TestParseTimeout_InvalidDuration(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set invalid duration string
	err := sel.Set("timeout", "invalid-duration")
	assert.NilError(t, err)

	// Should return error
	err = ParseTimeout(sel, "timeout")
	assert.ErrorContains(t, err, "parsing timeout failed")
}

func TestParseMemory_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set memory as string
	err := sel.Set("memory", "128MB")
	assert.NilError(t, err)

	// Parse memory
	err = ParseMemory(sel, "memory")
	assert.NilError(t, err)

	// Verify converted to bytes (128MB = 128000000 bytes in decimal)
	memory, err := sel.Get("memory")
	assert.NilError(t, err)
	assert.Equal(t, memory.(int64), int64(128000000))
}

func TestParseMemory_VariousSizes(t *testing.T) {
	testCases := []struct {
		name     string
		size     string
		expected int64
	}{
		{"1 GB", "1GB", 1000000000},
		{"512 MB", "512MB", 512000000},
		{"2 KB", "2KB", 2000},
		{"1 TB", "1TB", 1000000000000},
		{"256 bytes", "256B", 256},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			obj := object.New[object.Refrence]()
			sel := obj.Child("test")
			sel.Set("memory", tc.size)

			err := ParseMemory(sel, "memory")
			assert.NilError(t, err)

			memory, err := sel.Get("memory")
			assert.NilError(t, err)
			assert.Equal(t, memory.(int64), tc.expected)
		})
	}
}

func TestParseMemory_MissingField(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Field doesn't exist - should return no error
	err := ParseMemory(sel, "nonexistent")
	assert.NilError(t, err)
}

func TestParseMemory_NilField(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set field to nil explicitly
	err := sel.Set("memory", nil)
	assert.NilError(t, err)

	// Should return no error
	err = ParseMemory(sel, "memory")
	assert.NilError(t, err)
}

func TestParseMemory_InvalidType(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set memory as int instead of string
	err := sel.Set("memory", 123)
	assert.NilError(t, err)

	// Should return error
	err = ParseMemory(sel, "memory")
	assert.ErrorContains(t, err, "memory is not a string")
}

func TestParseMemory_InvalidSize(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set invalid size string
	err := sel.Set("memory", "invalid-size")
	assert.NilError(t, err)

	// Should return error
	err = ParseMemory(sel, "memory")
	assert.ErrorContains(t, err, "parsing memory failed")
}

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
