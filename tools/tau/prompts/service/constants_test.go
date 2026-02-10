package servicePrompts

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, NamePrompt, "Service Name:")
	assert.Equal(t, CreateThis, "Create this service?")
	assert.Equal(t, NoneFound, "no services found")
}
