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

// asRoot is scripts/as-root, on a machine where it can reach root without
// asking, as the Makefile's root targets need.
func asRoot(t *testing.T) string {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("these are about reaching root through sudo, and the test is root already")
	}
	if exec.Command("sudo", "-n", "true").Run() != nil {
		t.Skip("scripts/as-root needs passwordless sudo, and skips without it as the Makefile's root targets do")
	}
	script, err := filepath.Abs("as-root")
	require.NoError(t, err)
	return script
}

// said reads the KEY=value lines a command printed; sudo may say other things.
func said(out string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = value
		}
	}
	return values
}

func TestAsRootKeepsTheRunsTMPDIR(t *testing.T) {
	root := asRoot(t)
	c := newCapped(t)

	out := said(c.run(t, small, root, "sh", "-c",
		`echo "uid=$(id -u)"; echo "tmpdir=$TMPDIR"; echo "gotmpdir=${GOTMPDIR-unset}"; echo "cgroup=$(grep '^0::' /proc/self/cgroup)"`))

	assert.Equal(t, "0", out["uid"])
	scope := filepath.Base(out["cgroup"])
	id, ok := strings.CutPrefix(strings.TrimSuffix(scope, ".scope"), "tau-cap-")
	require.True(t, ok, "root's command must stay in the run's scope, and is in %s", out["cgroup"])
	assert.Equal(t, filepath.Join(c.ns, id), out["tmpdir"], "root runs in the run's TMPDIR")
	gotmpdir, set := os.LookupEnv("GOTMPDIR")
	if !set {
		gotmpdir = "unset"
	}
	assert.Equal(t, gotmpdir, out["gotmpdir"], "root has the GOTMPDIR the caller had, or none")
}

func TestAsRootKeepsGOTMPDIR(t *testing.T) {
	root := asRoot(t)
	tmp, gotmp := t.TempDir(), t.TempDir()

	cmd := exec.Command(root, "sh", "-c", `echo "uid=$(id -u)"; echo "tmpdir=$TMPDIR"; echo "gotmpdir=$GOTMPDIR"`)
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp, "GOTMPDIR="+gotmp)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)

	values := said(string(out))
	assert.Equal(t, "0", values["uid"])
	assert.Equal(t, tmp, values["tmpdir"])
	assert.Equal(t, gotmp, values["gotmpdir"])
}

// TestWhatARootRunLeavesIsRemovedByTheNextRun leaves a directory only root can
// remove in the run's TMPDIR, which is what a killed run under as-root leaves.
func TestWhatARootRunLeavesIsRemovedByTheNextRun(t *testing.T) {
	root := asRoot(t)
	c := newCapped(t)

	out := said(c.run(t, small, root, "sh", "-c",
		`mkdir -m 700 "$TMPDIR/root" && touch "$TMPDIR/root/file" && echo "tmpdir=$TMPDIR"`))
	tmpdir := out["tmpdir"]
	require.DirExists(t, filepath.Join(tmpdir, "root"))
	require.Error(t, os.RemoveAll(tmpdir), "only root can remove what root left")

	c.run(t, small, "true")

	assert.NoDirExists(t, filepath.Join(tmpdir, "root"))
}
