package e2e_tests

import (
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/cli"
	"gotest.tools/v3/assert"
)

// TestE2EBuildAndRunFunction builds the ping fixture with the real builder, then runs it
// with a random number in the query; asserts the response contains PONG and (n+1) to prove
// the same request/response round-trip. Skips when -short (requires container runtime).
func TestE2EBuildAndRunFunction(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e build+run requires container runtime, skip with -short")
	}
	dir, _ := WithE2EPingProjectEnv(t)
	outPath := filepath.Join(dir, "out.wasm")

	// Build
	err := cli.Run("tau", "build", "function", "--name", "ping", "-o", outPath)
	assert.NilError(t, err)

	n := time.Now().Unix() + int64(rand.Intn(100000))
	path := "/ping?n=" + strconv.FormatInt(n, 10)
	expectedPlusOne := strconv.FormatInt(n+1, 10)

	// Run: capture stdout so we can assert on PONG and (n+1)
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	pterm.SetDefaultOutput(w)
	defer func() {
		os.Stdout = oldStdout
		pterm.SetDefaultOutput(oldStdout)
	}()

	err = cli.Run("tau", "run", "function", "--name", "ping", "--wasm", outPath, "--path", path, "--defaults")
	w.Close()
	assert.NilError(t, err)

	out, err := io.ReadAll(r)
	assert.NilError(t, err)

	stdout := string(out)
	assert.Assert(t, strings.Contains(stdout, "PONG"), "run stdout should contain PONG, got: %s", stdout)
	assert.Assert(t, strings.Contains(stdout, expectedPlusOne), "run stdout should contain n+1=%s, got: %s", expectedPlusOne, stdout)
}
