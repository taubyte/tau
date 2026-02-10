package build

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestCommand(t *testing.T) {
	assert.Assert(t, Command != nil)
	assert.Equal(t, Command.Name, "build")
	assert.Assert(t, len(Command.Subcommands) > 0)
}
