package messagingPrompts

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, NamePrompt, "Messaging Name:")
	assert.Equal(t, CreateThis, "Create this messaging?")
	assert.Equal(t, NoneFound, "no messaging channels found")
}
