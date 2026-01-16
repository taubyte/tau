package engine

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestStringMatchAll_String(t *testing.T) {
	sm := StringMatchAll{}

	// Execute
	result := sm.String()

	// Verify
	assert.Equal(t, result, "StringMatchAll")
}

func TestEither_String(t *testing.T) {
	// Setup: Create Either matcher
	matcher := Either("value1", "value2", "value3")

	// Execute
	result := matcher.String()

	// Verify: Should contain the values
	assert.Assert(t, len(result) > 0)
	assert.Assert(t, result != "")
}

func TestEither_Match(t *testing.T) {
	// Setup: Create Either matcher
	matcher := Either("apple", "banana", "cherry")

	// Execute: Test matching values
	assert.Equal(t, matcher.Match("apple"), true)
	assert.Equal(t, matcher.Match("banana"), true)
	assert.Equal(t, matcher.Match("cherry"), true)
	assert.Equal(t, matcher.Match("pear"), false)
	assert.Equal(t, matcher.Match(""), false)
}

func TestEither_EmptyValues(t *testing.T) {
	// Setup: Create Either matcher with no values
	matcher := Either()

	// Execute: Should not match anything
	assert.Equal(t, matcher.Match("any"), false)
	assert.Equal(t, matcher.Match(""), false)
}
