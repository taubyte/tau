package applicationPrompts

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, NamePrompt, "Application Name:")
	assert.Equal(t, SelectPrompt, "Select an Application:")
	assert.Equal(t, CreateThis, "Create this application?")
	assert.Equal(t, DeleteThis, "Delete this application?")
	assert.Equal(t, EditThis, "Edit this application?")
	assert.Equal(t, NoneFound, "no applications found")
	assert.Assert(t, NotFound != "")
}
