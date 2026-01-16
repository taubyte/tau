package object

import (
	"bytes"
	"testing"

	"gotest.tools/v3/assert"
)

func TestNew(t *testing.T) {
	o := New[Opaque]()
	if _, ok := o.(*object[Opaque]); !ok {
		t.Errorf("Expected type *object[Opaque], got %T", o)
	}
}

func TestObjectSetGet(t *testing.T) {
	o := New[Opaque]()
	data := Opaque{1, 2, 3}
	o.Set("attr", data)
	if !bytes.Equal(o.Get("attr"), data) {
		t.Errorf("Expected data %v, got %v", data, o.Get("attr"))
	}
}

func TestMatch(t *testing.T) {
	o := New[Opaque]()
	o.Child("apple").Set("attr", Opaque{})
	o.Child("applepie").Set("attr", Opaque{})
	o.Child("pieapple").Set("attr", Opaque{})

	tests := []struct {
		expr  string
		mtype MatchType
		want  int
	}{
		{"apple", ExactMatch, 1},
		{"apple", PrefixMatch, 2},
		{"apple", SuffixMatch, 2},
		{"apple", SubMatch, 3},
		{"app.*pie", RegExMatch, 1},
	}

	for _, test := range tests {
		got, err := o.Match(test.expr, test.mtype)
		if err != nil {
			t.Errorf("Error on matching %s: %s", test.expr, err)
			continue
		}
		if len(got) != test.want {
			t.Errorf("For %s and type %v expected %d matches, got %d", test.expr, test.mtype, test.want, len(got))
		}
	}
}

func TestSelector(t *testing.T) {
	o := New[Opaque]()
	name := "testName"
	data := Opaque{4, 5, 6}
	selector := o.Child(name)
	if err := selector.Set("attr", data); err != nil {
		t.Errorf("Failed to set data on selector: %s", err)
	}
	if selector.Name() != name {
		t.Errorf("Expected name %s, got %s", name, selector.Name())
	}
	if !selector.Exists() {
		t.Error("Expected the selector to exist")
	}
	obj, err := selector.Object()
	if err != nil {
		t.Errorf("Failed to get object from selector: %s", err)
	}
	if !bytes.Equal(obj.Get("attr"), data) {
		t.Errorf("Expected data %v, got %v", data, obj.Get("attr"))
	}
	getData, err := selector.Get("attr")
	if err != nil {
		t.Errorf("Failed to get data from selector: %s", err)
	}
	if !bytes.Equal(getData, data) {
		t.Errorf("Expected data %v, got %v", data, getData)
	}
}

func TestSelectorFailures(t *testing.T) {
	o := New[Opaque]()
	selector := o.Child("nonExistent")
	if selector.Exists() {
		t.Error("Expected the selector to not exist")
	}
	if _, err := selector.Object(); err == nil {
		t.Error("Expected an error when getting object from non-existent selector")
	}
	if _, err := selector.Get("attr"); err == nil {
		t.Error("Expected an error when getting data from non-existent selector")
	}
}

func TestChildPanic(t *testing.T) {
	o := New[Opaque]()
	sel := o.Child(42) // This should return a selector with error
	if sel == nil {
		t.Error("Expected selector to be returned")
	}
	_, err := sel.Object()
	if err == nil {
		t.Error("Expected error when providing unknown object type")
	}
}

func TestGetObjectByName(t *testing.T) {
	o := New[Opaque]()
	childName := "child"
	child := New[Opaque]()
	o.Child(childName).Set("attr", child.Get("attr"))
	_, err := o.(*object[Opaque]).getObjectByName(childName)
	if err != nil {
		t.Errorf("Expected no error, but got %v", err)
	}

	_, err = o.(*object[Opaque]).getObjectByName("nonexistent")
	if err == nil {
		t.Error("Expected an error when getting object by nonexistent name")
	}
}

func TestObjectExists(t *testing.T) {
	o := New[Opaque]()
	childName := "child"
	child := New[Opaque]()
	o.Child(childName).Set("attr", child.Get("attr"))

	if !o.(*object[Opaque]).exists(childName, nil) {
		t.Errorf("Expected child %s to exist", childName)
	}

	if o.(*object[Opaque]).exists("nonexistent", nil) {
		t.Error("Expected child nonexistent to not exist")
	}

	otherChild := New[Opaque]()
	if o.(*object[Opaque]).exists("", otherChild.(*object[Opaque])) {
		t.Error("Expected otherChild to not exist")
	}
}

func TestSelectorExistingObject(t *testing.T) {
	o := New[Opaque]()
	childName := "child"
	childData := Opaque{7, 8, 9}
	child := o.Child(childName)
	child.Set("attr", childData)
	selectorForExisting := o.Child(childName)
	if selectorForExisting.Name() != childName {
		t.Errorf("Expected name %s, got %s", childName, selectorForExisting.Name())
	}
	if !selectorForExisting.Exists() {
		t.Error("Expected the selector for existing object to exist")
	}
	retrievedData, err := selectorForExisting.Get("attr")
	if err != nil {
		t.Errorf("Failed to get data from selector: %s", err)
	}
	if !bytes.Equal(retrievedData, childData) {
		t.Errorf("Expected data %v, got %v", childData, retrievedData)
	}
}

func TestObjectSetName(t *testing.T) {
	o := New[Opaque]()
	childName := "child"
	childData := Opaque{10, 11, 12}
	child := o.Child(childName)
	child.Set("attr", childData)
	newChildName := "renamedChild"
	o.Child(newChildName).Set("attr", childData)
	if o.(*object[Opaque]).exists(newChildName, nil) {
		ncdata, err := o.Child(newChildName).Get("attr")
		if err != nil {
			t.Error(err)
		}
		if !bytes.Equal(ncdata, childData) {
			t.Errorf("Expected data %v, got %v", childData, ncdata)
		}
	} else {
		t.Errorf("Child with name %s doesn't exist", newChildName)
	}
}

func TestChildWithPointer(t *testing.T) {
	o := New[Opaque]()
	childName := "child"
	child := o.Child(childName)
	child.Set("attr", Opaque{7, 8, 9})
	selectorFromPointer := o.Child(child.(*selector[Opaque]).obj)
	if selectorFromPointer.Name() != childName {
		t.Errorf("Expected name %s, got %s", childName, selectorFromPointer.Name())
	}
}

func TestSetExistingChild(t *testing.T) {
	o := New[Opaque]()
	childName := "child"
	childData1 := Opaque{7, 8, 9}
	childData2 := Opaque{10, 11, 12}
	o.Child(childName).Set("attr", childData1)
	o.Child(childName).Set("attr", childData2) // Overwriting
	retrievedData, _ := o.Child(childName).Get("attr")
	if !bytes.Equal(retrievedData, childData2) {
		t.Errorf("Expected data %v, got %v", childData2, retrievedData)
	}
}

func TestMatchUnknownType(t *testing.T) {
	o := New[Opaque]()
	_, err := o.Match("apple", MatchType(999)) // Unknown MatchType
	if err == nil {
		t.Error("Expected an error for unknown match type")
	}
}

func TestSetNameForExistingObject(t *testing.T) {
	o := New[Opaque]()
	childName := "child"
	childData := Opaque{7, 8, 9}
	o.Child(childName).Set("attr", childData)
	newChild := o.Child(childName).(*selector[Opaque]).obj
	o.Child(newChild).Set("attr", childData)
	if !o.(*object[Opaque]).exists(childName, newChild) {
		t.Errorf("Failed to set child with existing object")
	}
}

func TestExistsWithObjectReference(t *testing.T) {
	o := New[Opaque]()
	childName := "child"
	childData := Opaque{10, 11, 12}
	child := o.Child(childName)
	child.Set("attr", childData)
	if !o.(*object[Opaque]).exists("", child.(*selector[Opaque]).obj) {
		t.Errorf("Expected child %s to exist by object reference", childName)
	}
}

func TestChildPanicOnUnknownType(t *testing.T) {
	o := New[Opaque]()
	sel := o.Child(123) // This should return a selector with error
	if sel == nil {
		t.Error("Expected selector to be returned")
	}
	_, err := sel.Object()
	if err == nil {
		t.Errorf("Expected Child() to return error for unknown object type")
	}
}

func TestGetObjectByNameNonExistent(t *testing.T) {
	o := New[Opaque]()
	_, err := o.(*object[Opaque]).getObjectByName("nonExistentName")
	if err == nil || err != ErrNotExist {
		t.Errorf("Expected ErrNotExist for non-existent name got '%v'", err)
	}
}

func TestExistsByName(t *testing.T) {
	o := New[Opaque]()
	childName := "child"
	if o.(*object[Opaque]).exists(childName, nil) {
		t.Errorf("Expected child %s to not exist", childName)
	}
}

func TestObjectSetGetByName(t *testing.T) {
	o := New[Opaque]()

	name1 := "testName1"
	data1 := Opaque{1, 2, 3}
	o.Set(name1, data1)
	if got := o.Get(name1); !bytes.Equal(got, data1) {
		t.Errorf("For name %s, expected %v, got %v", name1, data1, got)
	}

	name2 := "testName2"
	data2 := Opaque("refValue")
	o.Set(name2, data2)
	if got := o.Get(name2); !bytes.Equal(got, data2) {
		t.Errorf("For name %s, expected %v, got %v", name2, data2, got)
	}
}

func TestObjectReference(t *testing.T) {
	o := New[Refrence]()

	refName := "refName"
	data := Refrence("Hello World")
	o.Set(refName, data)

	got := o.Get(refName)
	if got != data {
		t.Errorf("Expected reference %v, got %v", data, got)
	}
}

func TestSelectorForReference(t *testing.T) {
	o := New[Refrence]()
	name := "refName"
	data := Refrence("Hello Again")
	selector := o.Child(name)
	if err := selector.Set(name, data); err != nil {
		t.Errorf("Failed to set data on selector: %s", err)
	}
	if selector.Name() != name {
		t.Errorf("Expected name %s, got %s", name, selector.Name())
	}
	if !selector.Exists() {
		t.Error("Expected the selector to exist")
	}
	getData, err := selector.Get(name)
	if err != nil {
		t.Errorf("Failed to get data from selector: %s", err)
	}
	if getData != data {
		t.Errorf("Expected data %v, got %v", data, getData)
	}
}

func TestSelectorAdd(t *testing.T) {
	o := New[Opaque]()
	name := "newChild"
	selector := o.Child(name)

	// Confirm the child doesn't exist yet
	if selector.Exists() {
		t.Error("Child shouldn't exist yet")
	}

	// Add the child using the selector's Add() method
	err := selector.Add(o)
	assert.NilError(t, err, "Expected no error when adding a child")

	// Check if the child now exists
	if !selector.Exists() {
		t.Error("Child should exist after calling Add()")
	}

	// Fetch the object from the selector to ensure it was correctly added
	_, err = selector.Object()
	assert.NilError(t, err, "Expected no error when fetching the object after adding it")
}

func TestObjectChildren(t *testing.T) {
	o := New[Opaque]()
	childNames := []string{"child1", "child2", "child3"}

	// Add children to the object
	for _, name := range childNames {
		o.Child(name).Set("test", Opaque{})
	}

	// Retrieve child names using the Children() method
	gotChildren := o.Children()

	// Verify we got the correct number of children
	assert.Equal(t, len(gotChildren), len(childNames))

	// Verify each child name is present in the returned slice
	for _, name := range childNames {
		found := false
		for _, child := range gotChildren {
			if child == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected to find child name %s, but didn't", name)
		}
	}
}

func TestGetString(t *testing.T) {
	o := New[Refrence]()

	// Test successful GetString
	o.Set("name", Refrence("test-value"))
	val, err := o.GetString("name")
	assert.NilError(t, err)
	assert.Equal(t, val, "test-value")

	// Test GetString with non-existent key
	_, err = o.GetString("non-existent")
	assert.Error(t, err, ErrNotExist.Error())

	// Test GetString with wrong type
	o.Set("number", Refrence(42))
	_, err = o.GetString("number")
	assert.ErrorContains(t, err, "value is not a string")
}

func TestGetInt(t *testing.T) {
	o := New[Refrence]()

	// Test successful GetInt with int
	o.Set("int-val", Refrence(42))
	val, err := o.GetInt("int-val")
	assert.NilError(t, err)
	assert.Equal(t, val, 42)

	// Test GetInt with int8
	o.Set("int8-val", Refrence(int8(8)))
	val, err = o.GetInt("int8-val")
	assert.NilError(t, err)
	assert.Equal(t, val, 8)

	// Test GetInt with int64
	o.Set("int64-val", Refrence(int64(64)))
	val, err = o.GetInt("int64-val")
	assert.NilError(t, err)
	assert.Equal(t, val, 64)

	// Test GetInt with uint
	o.Set("uint-val", Refrence(uint(100)))
	val, err = o.GetInt("uint-val")
	assert.NilError(t, err)
	assert.Equal(t, val, 100)

	// Test GetInt with int16
	o.Set("int16-val", Refrence(int16(16)))
	val, err = o.GetInt("int16-val")
	assert.NilError(t, err)
	assert.Equal(t, val, 16)

	// Test GetInt with int32
	o.Set("int32-val", Refrence(int32(32)))
	val, err = o.GetInt("int32-val")
	assert.NilError(t, err)
	assert.Equal(t, val, 32)

	// Test GetInt with uint8
	o.Set("uint8-val", Refrence(uint8(8)))
	val, err = o.GetInt("uint8-val")
	assert.NilError(t, err)
	assert.Equal(t, val, 8)

	// Test GetInt with uint16
	o.Set("uint16-val", Refrence(uint16(16)))
	val, err = o.GetInt("uint16-val")
	assert.NilError(t, err)
	assert.Equal(t, val, 16)

	// Test GetInt with uint32
	o.Set("uint32-val", Refrence(uint32(32)))
	val, err = o.GetInt("uint32-val")
	assert.NilError(t, err)
	assert.Equal(t, val, 32)

	// Test GetInt with uint64
	o.Set("uint64-val", Refrence(uint64(64)))
	val, err = o.GetInt("uint64-val")
	assert.NilError(t, err)
	assert.Equal(t, val, 64)

	// Test GetInt with float32 that is a whole number
	o.Set("float32-whole", Refrence(float32(3.0)))
	val, err = o.GetInt("float32-whole")
	assert.NilError(t, err)
	assert.Equal(t, val, 3)

	// Test GetInt with float32 that has fractional part (should error)
	o.Set("float32-fraction", Refrence(float32(3.14)))
	_, err = o.GetInt("float32-fraction")
	assert.ErrorContains(t, err, "value is not an integer")

	// Test GetInt with float64 that is a whole number
	o.Set("float64-whole", Refrence(float64(3.0)))
	val, err = o.GetInt("float64-whole")
	assert.NilError(t, err)
	assert.Equal(t, val, 3)

	// Test GetInt with float64 that has fractional part (should error)
	o.Set("float64-fraction", Refrence(float64(3.14)))
	_, err = o.GetInt("float64-fraction")
	assert.ErrorContains(t, err, "value is not an integer")

	// Test GetInt with non-existent key
	_, err = o.GetInt("non-existent")
	assert.Error(t, err, ErrNotExist.Error())

	// Test GetInt with wrong type
	o.Set("string-val", Refrence("not-a-number"))
	_, err = o.GetInt("string-val")
	assert.ErrorContains(t, err, "value is not an integer")
}

func TestGetBool(t *testing.T) {
	o := New[Refrence]()

	// Test successful GetBool with true
	o.Set("true-val", Refrence(true))
	val, err := o.GetBool("true-val")
	assert.NilError(t, err)
	assert.Equal(t, val, true)

	// Test successful GetBool with false
	o.Set("false-val", Refrence(false))
	val, err = o.GetBool("false-val")
	assert.NilError(t, err)
	assert.Equal(t, val, false)

	// Test GetBool with non-existent key
	_, err = o.GetBool("non-existent")
	assert.Error(t, err, ErrNotExist.Error())

	// Test GetBool with wrong type
	o.Set("string-val", Refrence("not-a-bool"))
	_, err = o.GetBool("string-val")
	assert.ErrorContains(t, err, "value is not a boolean")
}

func TestSelectorGetString(t *testing.T) {
	o := New[Refrence]()
	selector := o.Child("child")

	// Test GetString on non-existent selector
	_, err := selector.GetString("attr")
	assert.Error(t, err, ErrNotExist.Error())

	// Test successful GetString
	selector.Set("attr", Refrence("test-value"))
	val, err := selector.GetString("attr")
	assert.NilError(t, err)
	assert.Equal(t, val, "test-value")

	// Test GetString with wrong type
	selector.Set("number", Refrence(42))
	_, err = selector.GetString("number")
	assert.ErrorContains(t, err, "value is not a string")
}

func TestSelectorGetInt(t *testing.T) {
	o := New[Refrence]()
	selector := o.Child("child")

	// Test GetInt on non-existent selector
	_, err := selector.GetInt("attr")
	assert.Error(t, err, ErrNotExist.Error())

	// Test successful GetInt
	selector.Set("attr", Refrence(42))
	val, err := selector.GetInt("attr")
	assert.NilError(t, err)
	assert.Equal(t, val, 42)

	// Test GetInt with float32 that is a whole number
	selector.Set("float32-whole", Refrence(float32(5.0)))
	val, err = selector.GetInt("float32-whole")
	assert.NilError(t, err)
	assert.Equal(t, val, 5)

	// Test GetInt with float32 that has fractional part (should error)
	selector.Set("float32-fraction", Refrence(float32(3.14)))
	_, err = selector.GetInt("float32-fraction")
	assert.ErrorContains(t, err, "value is not an integer")

	// Test GetInt with wrong type
	selector.Set("string-val", Refrence("not-a-number"))
	_, err = selector.GetInt("string-val")
	assert.ErrorContains(t, err, "value is not an integer")
}

func TestSelectorGetBool(t *testing.T) {
	o := New[Refrence]()
	selector := o.Child("child")

	// Test GetBool on non-existent selector
	_, err := selector.GetBool("attr")
	assert.Error(t, err, ErrNotExist.Error())

	// Test successful GetBool
	selector.Set("attr", Refrence(true))
	val, err := selector.GetBool("attr")
	assert.NilError(t, err)
	assert.Equal(t, val, true)

	// Test GetBool with wrong type
	selector.Set("string-val", Refrence("not-a-bool"))
	_, err = selector.GetBool("string-val")
	assert.ErrorContains(t, err, "value is not a boolean")
}

func TestSelectorGetStringWithError(t *testing.T) {
	o := New[Refrence]()
	// Create a selector with an error
	selector := o.Child(42) // This creates a selector with error

	_, err := selector.GetString("attr")
	assert.ErrorContains(t, err, "unknown object type")
}

func TestSelectorGetIntWithError(t *testing.T) {
	o := New[Refrence]()
	// Create a selector with an error
	selector := o.Child(42) // This creates a selector with error

	_, err := selector.GetInt("attr")
	assert.ErrorContains(t, err, "unknown object type")
}

func TestSelectorGetBoolWithError(t *testing.T) {
	o := New[Refrence]()
	// Create a selector with an error
	selector := o.Child(42) // This creates a selector with error

	_, err := selector.GetBool("attr")
	assert.ErrorContains(t, err, "unknown object type")
}
