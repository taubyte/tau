package engine

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestNodeToMap(t *testing.T) {
	node := &Node{
		Match: "test",
		Attributes: []*Attribute{
			{
				Name:    "attr1",
				Type:    TypeInt,
				Key:     true,
				Default: 123,
				Path:    []StringMatch{StringMatchAll{}},
				Compat:  []StringMatch{StringMatchAll{}},
			},
			{
				Name: "attr2",
				Type: TypeString,
				Path: []StringMatch{StringMatchAll{}},
			},
		},
		Children: []*Node{
			{
				Match: "child1",
			},
		},
	}

	expected := map[string]any{
		"match": "test",
		"group": false,
		"attributes": map[string]any{
			"attr1": map[string]any{
				"type":    "Int",
				"key":     true,
				"default": 123,
				"path":    stringify([]StringMatch{StringMatchAll{}}),
				"compat":  stringify([]StringMatch{StringMatchAll{}}),
			},
			"attr2": map[string]any{
				"type": "String",
				"path": stringify(StringMatchAll{}),
			},
		},
		"children": []any{
			map[string]any{
				"match":      "child1",
				"group":      false,
				"attributes": map[string]any{},
				"children":   []any{},
			},
		},
	}

	assert.DeepEqual(t, node.Map(), expected)
}

func TestNode_AttributesToMap_NoOptionalFields(t *testing.T) {
	// Use case: Testing attributesToMap without optional fields
	node := &Node{
		Attributes: []*Attribute{
			{
				Name: "attr1",
				Type: TypeInt,
				// No Key, Default, Path, or Compat
			},
		},
	}

	result := node.attributesToMap()

	assert.Assert(t, result != nil)
	attr1 := result["attr1"].(map[string]any)
	assert.Equal(t, attr1["type"], "Int")
	_, hasKey := attr1["key"]
	assert.Assert(t, !hasKey)
	_, hasDefault := attr1["default"]
	assert.Assert(t, !hasDefault)
	_, hasPath := attr1["path"]
	assert.Assert(t, !hasPath)
	_, hasCompat := attr1["compat"]
	assert.Assert(t, !hasCompat)
}

func TestNode_ChildrenToSlice(t *testing.T) {
	// Use case: Testing childrenToSlice
	child1 := &Node{Match: "child1"}
	child2 := &Node{Match: "child2", Group: true}

	node := &Node{
		Children: []*Node{child1, child2},
	}

	result := node.childrenToSlice()

	assert.Equal(t, len(result), 2)
	assert.Assert(t, result[0] != nil)
	assert.Assert(t, result[1] != nil)
}

func TestNode_Map_WithGroup(t *testing.T) {
	// Use case: Testing Map() with Group=true
	node := &Node{
		Group: true,
		Match: "group-match",
		Attributes: []*Attribute{
			{Name: "attr1", Type: TypeString},
		},
		Children: []*Node{
			{Match: "child1"},
		},
	}

	result := node.Map()

	assert.Assert(t, result != nil)
	assert.Equal(t, result["group"], true)
	assert.Equal(t, result["match"], "group-match")
}
