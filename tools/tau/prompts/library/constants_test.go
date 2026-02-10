package libraryPrompts

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, NamePrompt, "Library Name:")
	assert.Equal(t, CreateThis, "Create this library?")
	assert.Equal(t, NoneFound, "no libraries found")
}
