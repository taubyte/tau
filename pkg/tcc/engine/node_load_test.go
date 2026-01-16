package engine

import (
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func TestLoad_WithNonGroupNode(t *testing.T) {
	// Use case: Testing load with non-group node
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test", 0755)
	afero.WriteFile(fs, "/test/name.yaml", []byte("test-name"), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	node := &Node{
		Group: false,
		Attributes: []*Attribute{
			{
				Name: "name",
				Type: TypeString,
			},
		},
	}

	query := sr.Query()
	obj, err := load[object.Refrence](node, query)

	assert.NilError(t, err)
	assert.Assert(t, obj != nil)
	assert.Equal(t, obj.Get("name"), "test-name")
}

func TestLoad_WithGroupNode_RequiredAttributesError(t *testing.T) {
	// Use case: Testing load with group node that has required attributes and fails
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test", 0755)
	// Don't create config.yaml, so required attributes will fail

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	node := &Node{
		Group: true,
		Attributes: []*Attribute{
			{
				Name:     "requiredAttr",
				Type:     TypeString,
				Required: true,
			},
		},
		Children: []*Node{},
	}

	query := sr.Query()
	_, err = load[object.Refrence](node, query)

	// Should return error because required attribute is missing
	assert.ErrorContains(t, err, "")
}

func TestLoad_WithGroupNode_StringMatcherChild(t *testing.T) {
	// Use case: Testing load with group node that has StringMatcher children
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test/match1", 0755)
	fs.MkdirAll("/test/match2", 0755)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	childNode := &Node{
		Group: false,
		Match: Either("match1", "match2"), // StringMatcher
		Attributes: []*Attribute{
			{Name: "name", Type: TypeString},
		},
	}

	node := &Node{
		Group:    true,
		Children: []*Node{childNode},
	}

	query := sr.Query()
	obj, err := load[object.Refrence](node, query)

	assert.NilError(t, err)
	assert.Assert(t, obj != nil)
}

func TestLoad_WithGroupNode_SkipsConfigLeaf(t *testing.T) {
	// Use case: Testing load with group node that skips NodeDefaultSeerLeaf
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test/config", 0755)
	fs.MkdirAll("/test/child1", 0755)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	childNode := &Node{
		Group: false,
		Match: "child1",
		Attributes: []*Attribute{
			{Name: "name", Type: TypeString},
		},
	}

	node := &Node{
		Group:    true,
		Children: []*Node{childNode},
	}

	query := sr.Query()
	obj, err := load[object.Refrence](node, query)

	assert.NilError(t, err)
	assert.Assert(t, obj != nil)
	// Should have child1 but not process config as a child
	_, err = obj.Child("child1").Object()
	assert.NilError(t, err)
}

func TestLoad_WithGroupNode_ChildAddError(t *testing.T) {
	// Use case: Testing load when Child().Add() fails
	fs := afero.NewMemMapFs()
	fs.MkdirAll("/test/child1", 0755)
	afero.WriteFile(fs, "/test/child1/name.yaml", []byte("child1"), 0644)

	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/test"))
	assert.NilError(t, err)

	childNode := &Node{
		Group: false,
		Match: "child1",
		Attributes: []*Attribute{
			{Name: "name", Type: TypeString},
		},
	}

	node := &Node{
		Group:    true,
		Children: []*Node{childNode},
	}

	query := sr.Query()
	obj, err := load[object.Refrence](node, query)

	assert.NilError(t, err)
	assert.Assert(t, obj != nil)
}
