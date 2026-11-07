package generic_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

// With --defaults nothing is asked, so a required value no flag gave is an
// error naming its flag, and nothing is written.
func TestDefaultsMissingRequired(t *testing.T) {
	root := testutil.WithTCCFixtureCopyEnv(t)

	err := run("new", "function", "--name", "f", "--type", "http", "--call", "ping",
		"--domains", "test_domain1", "--timeout", "10s", "--memory", "16MB")
	assert.ErrorContains(t, err, "--paths is required")

	_, statErr := os.Stat(filepath.Join(root, "config", "functions", "f.yaml"))
	assert.Assert(t, os.IsNotExist(statErr), "nothing should have been written")
}
