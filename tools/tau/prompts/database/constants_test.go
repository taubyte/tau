package databasePrompts

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, NamePrompt, "Database Name:")
	assert.Equal(t, SelectPrompt, "Select a Database:")
	assert.Equal(t, CreateThis, "Create this database?")
	assert.Equal(t, NoneFound, "no databases found")
	assert.Assert(t, NotFound != "")
	assert.Assert(t, MinCannotBeGreaterThanMax != "")
}
