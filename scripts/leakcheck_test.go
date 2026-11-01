package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// leakcheck runs scripts/leakcheck in a TMPDIR of the test's own, which is
// where the binary's directory is made, and returns that TMPDIR with what was
// printed and the exit code.
func leakcheck(t *testing.T, args ...string) (tmp, out string, code int) {
	t.Helper()
	script, err := filepath.Abs("leakcheck")
	require.NoError(t, err)

	tmp = t.TempDir()
	cmd := exec.Command(script, args...)
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp, "GOTMPDIR=")
	combined, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return tmp, string(combined), exit.ExitCode()
	}
	require.NoError(t, err, "%s", combined)
	return tmp, string(combined), 0
}

func TestLeakcheckFailsABinaryThatLeavesAPeerOrAUniverse(t *testing.T) {
	for _, left := range []string{"tb-node-1234", "universe-5678"} {
		t.Run(left, func(t *testing.T) {
			tmp, out, code := leakcheck(t, "sh", "-c", `mkdir "$TMPDIR/`+left+`"`)

			assert.Equal(t, 1, code)
			assert.Contains(t, out, "LEAK: ")
			assert.Contains(t, out, left)
			assert.NoDirExists(t, filepath.Join(tmp, left))
			entries, err := os.ReadDir(tmp)
			require.NoError(t, err)
			assert.Empty(t, entries, "the binary's directory goes, leak and all")
		})
	}
}

func TestLeakcheckKeepsTheBinarysOwnFailure(t *testing.T) {
	_, _, code := leakcheck(t, "sh", "-c", `exit 3`)
	assert.Equal(t, 3, code)

	_, out, code := leakcheck(t, "sh", "-c", `mkdir "$TMPDIR/tb-node-1"; exit 3`)
	assert.Equal(t, 3, code)
	assert.Contains(t, out, "tb-node-1")
}

func TestLeakcheckGivesTheBinaryADirectoryOfItsOwn(t *testing.T) {
	tmp, out, code := leakcheck(t, "sh", "-c", `echo "$TMPDIR"; echo "$GOTMPDIR"; ls -A "$TMPDIR" | wc -l; mkdir "$TMPDIR/other"`)
	require.Equal(t, 0, code, out)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3, out)
	assert.Equal(t, tmp, filepath.Dir(lines[0]), "the binary's TMPDIR is in the run's")
	assert.Equal(t, tmp, lines[1], "t.TempDir stays where the run had it")
	assert.Equal(t, "0", strings.TrimSpace(lines[2]), "and starts empty")

	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	assert.Empty(t, entries, "whatever else the binary leaves goes with its directory")
}

func TestLeakcheckKeepsAGOTMPDIRTheCallerSet(t *testing.T) {
	script, err := filepath.Abs("leakcheck")
	require.NoError(t, err)
	gotmp := t.TempDir()

	cmd := exec.Command(script, "sh", "-c", `echo "$GOTMPDIR"`)
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir(), "GOTMPDIR="+gotmp)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Equal(t, gotmp, strings.TrimSpace(string(out)))
}

func TestGoTestFailsAPackageThatLeaksAPeer(t *testing.T) {
	gobin, err := exec.LookPath("go")
	require.NoError(t, err)
	script, err := filepath.Abs("leakcheck")
	require.NoError(t, err)

	module := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(module, "go.mod"), []byte("module leaky\n\ngo 1.27.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(module, "leaky_test.go"), []byte(`package leaky

import (
	"os"
	"testing"
)

func TestPassesAndLeaks(t *testing.T) {
	if _, err := os.MkdirTemp("", "tb-node-*"); err != nil {
		t.Fatal(err)
	}
}
`), 0o600))

	tmp := t.TempDir()
	cmd := exec.Command(gobin, "test", "-count=1", "-exec", script, "./...")
	cmd.Dir = module
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp, "GOTMPDIR=", "GOFLAGS=", "GOWORK=off")
	out, err := cmd.CombinedOutput()

	require.Error(t, err, "%s", out)
	assert.Contains(t, string(out), "LEAK: "+module)
	assert.Contains(t, string(out), "FAIL\tleaky")

	left, err := filepath.Glob(filepath.Join(tmp, "*", "tb-node-*"))
	require.NoError(t, err)
	assert.Empty(t, left)
}
