package version

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestVersion_Run(t *testing.T) {
	err := testutil.RunCommand(Command, "tau", "version")
	assert.NilError(t, err)
}
