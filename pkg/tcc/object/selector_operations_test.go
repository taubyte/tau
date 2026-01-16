package object

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestSelector_Rename(t *testing.T) {
	o := New[Refrence]()
	sel := o.Child("oldName")
	sel.Set("data", "value")

	err := sel.Rename("newName")
	assert.NilError(t, err)

	// Verify old name doesn't exist
	_, err = o.Child("oldName").Object()
	assert.Error(t, err, ErrNotExist.Error())

	// Verify new name exists
	newSel, err := o.Child("newName").Object()
	assert.NilError(t, err)

	data := newSel.Get("data")
	assert.Equal(t, data.(string), "value")
}

func TestSelector_RenameToSameName(t *testing.T) {
	o := New[Refrence]()
	sel := o.Child("name")
	sel.Set("data", "value")

	// Rename to same name should be no-op
	err := sel.Rename("name")
	assert.NilError(t, err)

	// Verify still exists
	_, err = o.Child("name").Object()
	assert.NilError(t, err)
}

func TestSelector_RenameConflict(t *testing.T) {
	o := New[Refrence]()
	sel1 := o.Child("name1")
	sel1.Set("data", "value1")

	sel2 := o.Child("name2")
	sel2.Set("data", "value2")

	// Try to rename name1 to name2 (conflict)
	err := sel1.Rename("name2")
	assert.ErrorContains(t, err, "already exists")
}

func TestSelector_RenameNonExistent(t *testing.T) {
	o := New[Refrence]()
	sel := o.Child("nonExistent")

	// Try to rename non-existent child
	err := sel.Rename("newName")
	assert.ErrorContains(t, err, "does not exist")
}

func TestSelector_Move(t *testing.T) {
	o := New[Refrence]()
	sel := o.Child("child")
	sel.Set("oldKey", "value")

	err := sel.Move("oldKey", "newKey")
	assert.NilError(t, err)

	// Verify old key doesn't exist
	obj, _ := sel.Object()
	_, exists := obj.(*object[Refrence]).data["oldKey"]
	assert.Assert(t, !exists)

	// Verify new key exists
	newValue, err := sel.Get("newKey")
	assert.NilError(t, err)
	assert.Equal(t, newValue.(string), "value")
}

func TestSelector_MoveNonExistent(t *testing.T) {
	o := New[Refrence]()
	sel := o.Child("child")
	sel.Set("existingKey", "value")

	err := sel.Move("nonExistent", "newKey")
	assert.Error(t, err, ErrNotExist.Error())
}

func TestSelector_Add(t *testing.T) {
	o := New[Refrence]()
	sel := o.Child("child")

	newObj := New[Refrence]()
	newObj.Set("data", "value")

	err := sel.Add(newObj)
	assert.NilError(t, err)

	// Verify object added
	obj, err := sel.Object()
	assert.NilError(t, err)

	data := obj.Get("data")
	assert.Equal(t, data.(string), "value")
}

func TestSelector_AddExisting(t *testing.T) {
	o := New[Refrence]()
	sel := o.Child("child")

	existingObj := New[Refrence]()
	existingObj.Set("oldData", "oldValue")
	sel.Add(existingObj)

	newObj := New[Refrence]()
	newObj.Set("newData", "newValue")

	// Add new object (should replace)
	err := sel.Add(newObj)
	assert.NilError(t, err)

	obj, _ := sel.Object()

	// Verify new data
	newData := obj.Get("newData")
	assert.Equal(t, newData.(string), "newValue")

	// Verify old data gone
	oldData := obj.Get("oldData")
	assert.Assert(t, oldData == nil)
}

func TestSelector_Delete(t *testing.T) {
	o := New[Refrence]()
	sel := o.Child("child")
	sel.Set("key1", "value1")
	sel.Set("key2", "value2")

	sel.Delete("key1")

	// Verify key1 deleted
	obj, _ := sel.Object()
	_, exists := obj.(*object[Refrence]).data["key1"]
	assert.Assert(t, !exists)

	// Verify key2 still exists
	value2, err := sel.Get("key2")
	assert.NilError(t, err)
	assert.Equal(t, value2.(string), "value2")
}

func TestSelector_DeleteNonExistent(t *testing.T) {
	o := New[Refrence]()
	sel := o.Child("child")
	sel.Set("existingKey", "value")

	// Delete non-existent key should not error
	sel.Delete("nonExistent")

	// Verify existing key still exists
	value, err := sel.Get("existingKey")
	assert.NilError(t, err)
	assert.Equal(t, value.(string), "value")
}
