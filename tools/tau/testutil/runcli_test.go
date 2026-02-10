package testutil

import (
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/cli"
	"gotest.tools/v3/assert"
)

func TestRunCLI_Version(t *testing.T) {
	// Version command doesn't require project or auth.
	stdout, stderr, err := RunCLI(t, cli.Run, "", "version")
	assert.NilError(t, err)
	assert.Equal(t, stderr, "")
	assert.Assert(t, strings.Contains(stdout, "version") || strings.Contains(stdout, "Version") || len(stdout) > 0,
		"expected version output: %s", stdout)
}
