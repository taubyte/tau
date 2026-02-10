package storagePrompts

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, NamePrompt, "Storage Name:")
	assert.Equal(t, CreateThis, "Create this storage?")
	assert.Equal(t, NoneFound, "no storages found")
}
