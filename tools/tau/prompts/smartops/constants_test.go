package smartopsPrompts

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, NamePrompt, "SmartOps Name:")
	assert.Equal(t, CreateThis, "Create this smartops?")
	assert.Equal(t, NoneFound, "no smartops found")
}
