package engine

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestDefineIterGroup(t *testing.T) {
	// Setup: Create attributes and children
	attrs := []*Attribute{
		{Name: "attr1", Type: TypeString},
		{Name: "attr2", Type: TypeInt},
	}

	child1 := &Node{Group: false}
	child2 := &Node{Group: true}

	// Execute
	node := DefineIterGroup(attrs, child1, child2)

	// Verify
	assert.Assert(t, node != nil)
	assert.Equal(t, node.Group, true)
	assert.Equal(t, len(node.Attributes), 2)
	assert.Equal(t, len(node.Children), 2)
	assert.Equal(t, node.Attributes[0].Name, "attr1")
	assert.Equal(t, node.Attributes[1].Name, "attr2")
	assert.Assert(t, node.Children[0] == child1)
	assert.Assert(t, node.Children[1] == child2)
}

func TestFloat_Helper(t *testing.T) {
	// Use case: Testing Float helper function
	attr := Float("myFloat", Key(), Default(3.14))

	assert.Equal(t, attr.Name, "myFloat")
	assert.Equal(t, attr.Type, TypeFloat)
	assert.Equal(t, attr.Key, true)
	assert.Equal(t, attr.Default, 3.14)
}

func TestStringSlice_Helper(t *testing.T) {
	// Use case: Testing StringSlice helper function
	defaultVal := []string{"item1", "item2"}
	attr := StringSlice("mySlice", Required(), Default(defaultVal))

	assert.Equal(t, attr.Name, "mySlice")
	assert.Equal(t, attr.Type, TypeStringSlice)
	assert.Equal(t, attr.Required, true)
	// Compare slice elements individually since slices aren't directly comparable
	defaultSlice := attr.Default.([]string)
	assert.Equal(t, len(defaultSlice), 2)
	assert.Equal(t, defaultSlice[0], "item1")
	assert.Equal(t, defaultSlice[1], "item2")
}

func TestDefine_Helper(t *testing.T) {
	// Use case: Testing Define helper function
	attrs := []*Attribute{
		{Name: "attr1", Type: TypeString},
	}
	child := &Node{Match: "child"}

	node := Define("match-name", attrs, child)

	assert.Assert(t, node != nil)
	assert.Equal(t, node.Group, false)
	assert.Equal(t, node.Match, "match-name")
	assert.Equal(t, len(node.Attributes), 1)
	assert.Equal(t, len(node.Children), 1)
}
