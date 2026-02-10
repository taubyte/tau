package functionPrompts

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, NamePrompt, "Function Name:")
	assert.Equal(t, SelectPrompt, "Select a Function:")
	assert.Equal(t, CreateThis, "Create this function?")
	assert.Equal(t, NoneFound, "no functions found")
	assert.Assert(t, NotFound != "")
}
