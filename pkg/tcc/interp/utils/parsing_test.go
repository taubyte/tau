package utils

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

func TestRenameById_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("originalName")

	err := sel.Set("id", "resource-id-123")
	assert.NilError(t, err)

	idStr, err := RenameById(sel, "newName", "id", "name")
	assert.NilError(t, err)
	assert.Equal(t, idStr, "resource-id-123")

	renamedSel := obj.Child("resource-id-123")
	exists := renamedSel.Exists()
	assert.Assert(t, exists)

	name, err := renamedSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "newName")

	idValue, err := renamedSel.Get("id")
	assert.NilError(t, err)
	assert.Assert(t, idValue == nil)
}

func TestRenameById_MissingId(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	_, err := RenameById(sel, "name", "id", "name")
	assert.ErrorContains(t, err, "fetching id failed")
}

func TestRenameById_InvalidIdType(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	err := sel.Set("id", 123)
	assert.NilError(t, err)

	_, err = RenameById(sel, "name", "id", "name")
	assert.ErrorContains(t, err, "id is not a string")
}

func TestRenameById_DuplicateName(t *testing.T) {
	obj := object.New[object.Refrence]()

	sel1 := obj.Child("first")
	sel1.Set("id", "id-1")

	sel2 := obj.Child("second")
	sel2.Set("id", "id-1")

	_, err := RenameById(sel1, "name1", "id", "name")
	assert.NilError(t, err)

	_, err = RenameById(sel2, "name2", "id", "name")
	assert.ErrorContains(t, err, "renaming to id failed")
}

func TestRenameById_CustomFieldName(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("originalName")

	err := sel.Set("id", "custom-id-456")
	assert.NilError(t, err)

	idStr, err := RenameById(sel, "customName", "id", "name")
	assert.NilError(t, err)
	assert.Equal(t, idStr, "custom-id-456")

	renamedSel := obj.Child("custom-id-456")
	name, err := renamedSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "customName")
}

func TestRenameByName_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("resource-id-123")

	err := sel.Set("name", "my-resource")
	assert.NilError(t, err)

	nameStr, err := RenameByName(sel, "id", "name")
	assert.NilError(t, err)
	assert.Equal(t, nameStr, "my-resource")

	renamedSel := obj.Child("my-resource")
	exists := renamedSel.Exists()
	assert.Assert(t, exists)

	id, err := renamedSel.Get("id")
	assert.NilError(t, err)
	assert.Equal(t, id.(string), "resource-id-123")

	nameValue, err := renamedSel.Get("name")
	assert.NilError(t, err)
	assert.Assert(t, nameValue == nil)
}

func TestRenameByName_MissingName(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	_, err := RenameByName(sel, "id", "name")
	assert.ErrorContains(t, err, "fetching name failed")
}

func TestRenameByName_InvalidNameType(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("test")

	err := sel.Set("name", 123)
	assert.NilError(t, err)

	_, err = RenameByName(sel, "id", "name")
	assert.ErrorContains(t, err, "name is not a string")
}

func TestRenameByName_EmptyKey(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("")

	err := sel.Set("name", "my-resource")
	assert.NilError(t, err)

	_, err = RenameByName(sel, "id", "name")
	assert.ErrorContains(t, err, "current key is empty")
}

func TestRenameByName_DuplicateName(t *testing.T) {
	obj := object.New[object.Refrence]()

	sel1 := obj.Child("id-1")
	sel1.Set("name", "same-name")

	sel2 := obj.Child("id-2")
	sel2.Set("name", "same-name")

	_, err := RenameByName(sel1, "id", "name")
	assert.NilError(t, err)

	_, err = RenameByName(sel2, "id", "name")
	assert.ErrorContains(t, err, "renaming to name failed")
}
