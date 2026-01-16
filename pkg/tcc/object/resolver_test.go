package object

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestResolveFunction(t *testing.T) {
	root := New[Refrence]()
	root.Child("child1").Add(New[Refrence]())
	ch, _ := root.Child("child1").Object()
	ch.Child("child2").Add(New[Refrence]())
	resolver := NewResolver(root)
	obj, err := resolver.Resolve("child1", "child2")
	assert.NilError(t, err, "Expected no error")
	assert.Assert(t, obj != nil, "Expected object, got nil")
}
