package websitePrompts

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, NamePrompt, "Website Name:")
	assert.Equal(t, CreateThis, "Create this website?")
	assert.Equal(t, NoneFound, "no websites found")
}
