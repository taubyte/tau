package domainPrompts

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, NamePrompt, "Domain Name:")
	assert.Equal(t, SelectPrompt, "Select a Domain:")
	assert.Equal(t, CreateThis, "Create this domain?")
	assert.Equal(t, NoneFound, "no domains found")
	assert.Assert(t, NotFound != "")
}
