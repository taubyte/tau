package engine

import (
	"errors"
	"testing"

	"gotest.tools/v3/assert"
)

func TestStringify_WithAttributeValidator(t *testing.T) {
	// Use case: Testing stringify with AttributeValidator
	validator := func(any) error {
		return errors.New("test")
	}

	result := stringify(AttributeValidator(validator))

	assert.Equal(t, result, "AttributeValidator()")
}

func TestStringify_WithStringMatchSlice(t *testing.T) {
	// Use case: Testing stringify with []StringMatch
	matches := []StringMatch{
		"path1",
		Either("value1", "value2"),
		"path3",
	}

	result := stringify(matches)

	// Should contain all paths joined with "/"
	assert.Assert(t, len(result) > 0)
	assert.Assert(t, result != "unknown")
}

func TestStringify_WithUnknownType(t *testing.T) {
	// Use case: Testing stringify with unknown type
	result := stringify(12345)

	assert.Equal(t, result, "unknown")
}
