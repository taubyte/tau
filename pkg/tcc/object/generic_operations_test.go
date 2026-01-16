package object

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestObject_Move(t *testing.T) {
	o := New[Refrence]()
	o.Set("oldKey", "value")

	err := o.Move("oldKey", "newKey")
	assert.NilError(t, err)

	// Verify old key doesn't exist (Get returns zero value)
	oldValue := o.Get("oldKey")
	assert.Assert(t, oldValue == nil)

	// Verify new key has the value
	value := o.Get("newKey")
	assert.Equal(t, value.(string), "value")
}

func TestObject_MoveNonExistent(t *testing.T) {
	o := New[Refrence]()

	err := o.Move("nonExistent", "newKey")
	assert.Error(t, err, ErrNotExist.Error())
}

func TestObject_Fetch(t *testing.T) {
	o := New[Refrence]()
	child1, _ := o.CreatePath("child1")
	child2, _ := child1.CreatePath("child2")
	child2.Set("data", "value")

	// Fetch nested path
	fetched, err := o.Fetch("child1", "child2")
	assert.NilError(t, err)

	data := fetched.Get("data")
	assert.Equal(t, data.(string), "value")
}

func TestObject_FetchNonExistent(t *testing.T) {
	o := New[Refrence]()

	_, err := o.Fetch("nonExistent")
	assert.Error(t, err, ErrNotExist.Error())
}

func TestObject_FetchEmptyPath(t *testing.T) {
	o := New[Refrence]()
	o.Set("data", "value")

	// Fetch with empty path should return self
	fetched, err := o.Fetch()
	assert.NilError(t, err)
	assert.Assert(t, fetched == o)
}

func TestObject_CreatePath(t *testing.T) {
	o := New[Refrence]()

	// Create nested path
	created, err := o.CreatePath("level1", "level2", "level3")
	assert.NilError(t, err)
	assert.Assert(t, created != nil)

	// Verify path exists
	fetched, err := o.Fetch("level1", "level2", "level3")
	assert.NilError(t, err)
	assert.Assert(t, fetched == created)
}

func TestObject_CreatePathExisting(t *testing.T) {
	o := New[Refrence]()

	// Create path first time
	created1, err := o.CreatePath("existing", "path")
	assert.NilError(t, err)
	created1.Set("data", "value1")

	// Create same path again (should return existing)
	created2, err := o.CreatePath("existing", "path")
	assert.NilError(t, err)

	// Should be the same object
	data := created2.Get("data")
	assert.Equal(t, data.(string), "value1")
}

func TestObject_CreatePathEmpty(t *testing.T) {
	o := New[Refrence]()

	// Create path with empty path should return self
	created, err := o.CreatePath()
	assert.NilError(t, err)
	assert.Assert(t, created == o)
}

func TestObject_Delete(t *testing.T) {
	o := New[Refrence]()
	o.Set("key1", "value1")
	o.Set("key2", "value2")

	o.Delete("key1")

	// Verify key1 deleted
	value1 := o.Get("key1")
	assert.Assert(t, value1 == nil)

	// Verify key2 still exists
	value2 := o.Get("key2")
	assert.Equal(t, value2.(string), "value2")
}

func TestObject_Map(t *testing.T) {
	o := New[Refrence]()
	o.Set("attr1", "value1")
	o.Set("attr2", "value2")

	child, _ := o.CreatePath("child")
	child.Set("childAttr", "childValue")

	m := o.Map()

	// Verify attributes in map
	attrs := m["attributes"].(map[string]Refrence)
	assert.Equal(t, attrs["attr1"].(string), "value1")
	assert.Equal(t, attrs["attr2"].(string), "value2")

	// Verify child in map
	_, exists := m["child"]
	assert.Assert(t, exists)
}

func TestObject_Flat(t *testing.T) {
	o := New[Refrence]()
	o.Set("attr1", "value1")
	o.Set("attr2", "value2")

	child, _ := o.CreatePath("child")
	child.Set("childAttr", "childValue")

	flat := o.Flat()

	// Verify attributes in flat map
	assert.Equal(t, flat["attr1"].(string), "value1")
	assert.Equal(t, flat["attr2"].(string), "value2")

	// Verify child in flat map (as nested flat map)
	childFlat := flat["child"].(map[string]any)
	assert.Equal(t, childFlat["childAttr"].(string), "childValue")
}
