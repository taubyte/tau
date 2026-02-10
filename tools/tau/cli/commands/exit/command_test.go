package exit

import (
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestCommand(t *testing.T) {
	assert.Assert(t, Command != nil)
	assert.Equal(t, Command.Name, "tau")
	assert.Equal(t, Command.Usage, "Clears the current session")
	assert.Assert(t, Command.Action != nil)
}

func TestRun_ExitRuns(t *testing.T) {
	// Run exit command. When no session exists we get "session not found"; when one exists (e.g. -race), Delete() succeeds.
	err := testutil.RunCommand(Command, "tau", "exit")
	if err != nil {
		assert.Assert(t, strings.Contains(err.Error(), "session") || strings.Contains(err.Error(), "not found"),
			"expected session/not found in error: %s", err.Error())
	}
}
