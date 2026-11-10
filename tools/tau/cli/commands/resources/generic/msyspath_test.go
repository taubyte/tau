package generic_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

// Git Bash rewrites "/fn1" to "C:/Program Files/Git/fn1" before tau sees it;
// the function must still get the URL path "/fn1". Stdout carries no
// "[paths trace]" debug lines by default.
func TestMSYSConvertedPath(t *testing.T) {
	root := testutil.WithTCCFixtureCopyEnv(t)
	t.Setenv("TAU_DEBUG", "")

	r, w, err := os.Pipe()
	assert.NilError(t, err)
	stdout := os.Stdout
	os.Stdout = w
	err = cli.Run("tau", "--defaults", "--yes", "--color", "never",
		"new", "function", "msys_fn", "--type", "https", "--method", "GET",
		"--paths", "C:/Program Files/Git/fn1", "--domains", "test_domain1",
		"--timeout", "10s", "--memory", "16MB", "--call", "ping", "--source", ".")
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	assert.NilError(t, err)
	assert.Assert(t, !strings.Contains(string(out), "trace"), string(out))

	yaml := read(t, filepath.Join(root, "config", "functions", "msys_fn.yaml"))
	assert.Assert(t, strings.Contains(yaml, "- /fn1"), yaml)
	assert.Assert(t, !strings.Contains(yaml, "Program Files"), yaml)
}
