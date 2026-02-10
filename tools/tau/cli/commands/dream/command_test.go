package dream

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestCommand(t *testing.T) {
	assert.Assert(t, Command != nil)
	assert.Equal(t, Command.Name, "dream")
	assert.Assert(t, Command.Action != nil)
	assert.Assert(t, len(Command.Subcommands) > 0)
}

func TestConstants(t *testing.T) {
	assert.Assert(t, defaultBind != "")
	assert.Assert(t, len(cacheDream) > 0)
}

func TestRun_NoProject(t *testing.T) {
	// Running dream without a selected project returns an error
	err := testutil.RunCommand(Command, "tau", "dream")
	assert.Assert(t, err != nil)
}
