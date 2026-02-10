package current

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestCommand(t *testing.T) {
	assert.Assert(t, Command != nil)
	assert.Equal(t, Command.Name, "current")
	assert.Assert(t, Command.Action != nil)
}

func TestRun_NoSessionOrConfig(t *testing.T) {
	// Run should not panic when no session/config; may return (none) for all values
	err := testutil.RunCommand(Command, "tau", "current")
	assert.NilError(t, err)
}

func TestRun_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	err := testutil.RunCommand(Command, "tau", "current")
	assert.NilError(t, err)
	// With TCC fixture, selected project is set so Run renders table with project name
}
