package autocomplete

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestCommand(t *testing.T) {
	assert.Assert(t, Command != nil)
	assert.Equal(t, Command.Name, "autocomplete")
	assert.Assert(t, Command.Action != nil)
}

func TestRun_PrintsScriptAndBinaryName(t *testing.T) {
	// Run prints embedded script + basePath; capture via RunCommand (stdout not captured by testutil)
	// So run and just ensure no error; script is embedded and printed
	err := testutil.RunCommand(Command, "tau", "autocomplete")
	assert.NilError(t, err)
}
