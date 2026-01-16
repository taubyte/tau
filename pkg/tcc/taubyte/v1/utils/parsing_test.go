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

func TestFormatTimeout_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set timeout as nanoseconds (5 seconds = 5000000000ns)
	err := sel.Set("timeout", int64(5000000000))
	assert.NilError(t, err)

	// Format timeout
	err = FormatTimeout(sel, "timeout")
	assert.NilError(t, err)

	// Verify converted to duration string
	timeout, err := sel.Get("timeout")
	assert.NilError(t, err)
	assert.Equal(t, timeout.(string), "5s")
}

func TestFormatTimeout_VariousTypes(t *testing.T) {
	testCases := []struct {
		name     string
		value    interface{}
		expected string
	}{
		{"int64", int64(3600000000000), "1h0m0s"},
		{"int", int(1800000000000), "30m0s"},
		{"int32", int32(2000000000), "2s"},
		{"500ms", int64(500000000), "500ms"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			obj := object.New[object.Refrence]()
			sel := obj.Child("test")
			sel.Set("timeout", tc.value)

			err := FormatTimeout(sel, "timeout")
			assert.NilError(t, err)

			timeout, err := sel.Get("timeout")
			assert.NilError(t, err)
			assert.Equal(t, timeout.(string), tc.expected)
		})
	}
}

func TestFormatTimeout_MissingField(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Field doesn't exist - should return no error
	err := FormatTimeout(sel, "nonexistent")
	assert.NilError(t, err)
}

func TestFormatTimeout_NilField(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set field to nil explicitly
	err := sel.Set("timeout", nil)
	assert.NilError(t, err)

	// Should return no error
	err = FormatTimeout(sel, "timeout")
	assert.NilError(t, err)
}

func TestFormatTimeout_InvalidType(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set timeout as string instead of integer
	err := sel.Set("timeout", "5s")
	assert.NilError(t, err)

	// Should return error
	err = FormatTimeout(sel, "timeout")
	assert.ErrorContains(t, err, "timeout is not an integer")
}

func TestFormatMemory_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set memory as bytes (128MB = 128000000 bytes in decimal)
	err := sel.Set("memory", int64(128000000))
	assert.NilError(t, err)

	// Format memory
	err = FormatMemory(sel, "memory")
	assert.NilError(t, err)

	// Verify converted to human-readable string
	memory, err := sel.Get("memory")
	assert.NilError(t, err)
	memoryStr := memory.(string)
	assert.Assert(t, len(memoryStr) > 0, "memory should be formatted as string")
}

func TestFormatMemory_VariousTypes(t *testing.T) {
	testCases := []struct {
		name  string
		value interface{}
	}{
		{"int64", int64(1000000000)}, // 1GB
		{"int", int(512000000)},      // 512MB
		{"int32", int32(2000)},       // 2KB
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			obj := object.New[object.Refrence]()
			sel := obj.Child("test")
			sel.Set("memory", tc.value)

			err := FormatMemory(sel, "memory")
			assert.NilError(t, err)

			memory, err := sel.Get("memory")
			assert.NilError(t, err)
			assert.Assert(t, memory.(string) != "", "memory should be formatted as string")
		})
	}
}

func TestFormatMemory_MissingField(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Field doesn't exist - should return no error
	err := FormatMemory(sel, "nonexistent")
	assert.NilError(t, err)
}

func TestFormatMemory_NilField(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set field to nil explicitly
	err := sel.Set("memory", nil)
	assert.NilError(t, err)

	// Should return no error
	err = FormatMemory(sel, "memory")
	assert.NilError(t, err)
}

func TestFormatMemory_InvalidType(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set memory as string instead of integer
	err := sel.Set("memory", "128MB")
	assert.NilError(t, err)

	// Should return error
	err = FormatMemory(sel, "memory")
	assert.ErrorContains(t, err, "memory is not an integer")
}

func TestFormatSize_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	// Set size as bytes (2GB = 2000000000 bytes in decimal)
	err := sel.Set("size", int64(2000000000))
	assert.NilError(t, err)

	// Format size (should call FormatMemory)
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
