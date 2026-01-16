package engine

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestNode_ChildMatch_StringMatch(t *testing.T) {
	// Setup: Create node with string match children
	child1 := &Node{Match: "child1"}
	child2 := &Node{Match: "child2"}

	parent := &Node{
		Children: []*Node{child1, child2},
	}

	// Execute: Match existing child
	matched, err := parent.ChildMatch("child1")

	// Verify
	assert.NilError(t, err)
	assert.Assert(t, matched == child1)
}

func TestNode_ChildMatch_StringMatcher(t *testing.T) {
	// Setup: Create node with StringMatcher children
	matcher := All()
	child1 := &Node{Match: matcher}
	child2 := &Node{Match: "exact"}

	parent := &Node{
		Children: []*Node{child1, child2},
	}

	// Execute: Match using StringMatcher
	matched, err := parent.ChildMatch("any-string")

	// Verify: Should match the All() matcher
	assert.NilError(t, err)
	assert.Assert(t, matched == child1)
}

func TestNode_ChildMatch_NotFound(t *testing.T) {
	// Setup: Create node with children
	child1 := &Node{Match: "child1"}
	parent := &Node{
		Children: []*Node{child1},
	}

	// Execute: Match non-existent child
	_, err := parent.ChildMatch("nonexistent")

	// Verify: Should return error
	assert.Error(t, err, "not found")
}

func TestNode_ChildMatch_EmptyChildren(t *testing.T) {
	// Setup: Create node with no children
	parent := &Node{
		Children: []*Node{},
	}

	// Execute: Match any child
	_, err := parent.ChildMatch("any")

	// Verify: Should return error
	assert.Error(t, err, "not found")
}
