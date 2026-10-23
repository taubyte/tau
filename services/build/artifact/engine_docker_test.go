//go:build docker_integration

package artifact_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/build/artifact"
)

// needsEgressRestriction FAILS when this machine cannot confine a container's
// network, and says what is missing.
//
// IT IS AN ENVIRONMENT CAPABILITY AND NOT A CHOICE. A build runs a stranger's
// commands, so pkg/containers installs a firewall and REFUSES TO START THE
// CONTAINER if it cannot — a rootless docker keeps its bridge in its own
// namespace, and a process without CAP_NET_ADMIN cannot program it at all. That
// fail-closed behaviour is the property; a test that turned the restriction off
// to get green would be testing a build this service will never run.
//
// IT USED TO SKIP, and a skipped run prints `ok`: every machine that could not
// build reported these as passing, having built nothing. They are selected by a
// build tag, so whoever runs them asked for a real build — `make test-build`
// gives them a rootful daemon and the privilege.
func needsEgressRestriction(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "egress") {
		t.Fatalf("this machine cannot restrict a container's egress, so no build runs here (rootful docker and CAP_NET_ADMIN are needed; use `make test-build`): %v", err)
	}
}

// A REAL BUILD, IN A REAL CONTAINER, tagged the way every other test in this
// tree that needs a daemon is. What it proves is the part no fake can: that the
// checkout is bind-mounted where the commands expect it, that the commands run
// in order and stop at the first failure, that the build's own output reaches
// the job's log, and that what it wrote is read back off the host.
func TestARealBuild(t *testing.T) {
	e, err := artifact.NewEngine(nil)
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input"), []byte("hello"), 0o644))

	var log strings.Builder
	product, err := e.Make(t.Context(), artifact.Spec{
		Image:     "busybox:1.36",
		Dir:       dir,
		Commands:  []string{"echo building", "mkdir -p out", "cp input out/app.wasm"},
		Variables: map[string]string{"GREETING": "hi"},
		Output:    "out/app.wasm",
	}, &log)
	needsEgressRestriction(t, err)
	require.NoError(t, err)
	defer product.Close()

	body, err := io.ReadAll(product)
	require.NoError(t, err)
	require.Equal(t, "hello", string(body))
	require.Contains(t, log.String(), "building")
}

// THE FIRST COMMAND THAT FAILS ENDS THE BUILD, which is what anybody writing
// two lines of a build expects, and the container's exit code is what reports
// it.
func TestARealBuildThatFails(t *testing.T) {
	e, err := artifact.NewEngine(nil)
	require.NoError(t, err)

	var log strings.Builder
	_, err = e.Make(t.Context(), artifact.Spec{
		Image:    "busybox:1.36",
		Dir:      t.TempDir(),
		Commands: []string{"echo compiling", "echo main.go:3: undefined: x >&2", "false", "echo this must not run > out"},
		Output:   "out",
	}, &log)
	needsEgressRestriction(t, err)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "left nothing at",
		"it failed at the command, not at the missing output")
	require.NotContains(t, log.String(), "this must not run")
	require.Contains(t, log.String(), "compiling")
	require.Contains(t, log.String(), "main.go:3: undefined: x")
}

// A BUILD WHOSE COMMANDS SUCCEEDED AND PUT THE PRODUCT SOMEWHERE ELSE is the
// most common way a build fails without failing.
func TestARealBuildThatLeavesNothing(t *testing.T) {
	e, err := artifact.NewEngine(nil)
	require.NoError(t, err)

	var log strings.Builder
	_, err = e.Make(t.Context(), artifact.Spec{
		Image:    "busybox:1.36",
		Dir:      t.TempDir(),
		Commands: []string{"echo done"},
		Output:   "target/app.wasm",
	}, &log)
	needsEgressRestriction(t, err)
	require.ErrorContains(t, err, "left nothing at")
}

// A VARIABLE THE AUTHOR DECLARED FOR THE BUILD REACHES IT.
func TestARealBuildSeesItsVariables(t *testing.T) {
	e, err := artifact.NewEngine(nil)
	require.NoError(t, err)

	var log strings.Builder
	product, err := e.Make(t.Context(), artifact.Spec{
		Image:     "busybox:1.36",
		Dir:       t.TempDir(),
		Commands:  []string{"printf %s \"$GOPRIVATE\" > out"},
		Variables: map[string]string{"GOPRIVATE": "github.com/acme/*"},
		Output:    "out",
	}, &log)
	needsEgressRestriction(t, err)
	require.NoError(t, err)
	defer product.Close()

	body, err := io.ReadAll(product)
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/*", string(body))
}

// A REAL BUILD IS LOGGED WHILE IT RUNS: its first line reaches the log while
// the container is still waiting for the test to let it finish.
func TestARealBuildLogsWhileItRuns(t *testing.T) {
	e, err := artifact.NewEngine(nil)
	require.NoError(t, err)

	dir := t.TempDir()
	log := &signal{want: "compiling", seen: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		product, err := e.Make(t.Context(), artifact.Spec{
			Image:    "busybox:1.36",
			Dir:      dir,
			Commands: []string{"echo compiling", "while [ ! -f release ]; do sleep 0.1; done", "echo linked", "printf built > out"},
			Output:   "out",
		}, log)
		if err == nil {
			product.Close()
		}
		done <- err
	}()

	select {
	case <-log.seen:
	case err := <-done:
		needsEgressRestriction(t, err)
		t.Fatalf("the build ended before its first line was logged: %v", err)
	case <-time.After(2 * time.Minute):
		t.Fatal("the first line must reach the log while the container runs")
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "release"), nil, 0o644))
	require.NoError(t, <-done)
	require.Contains(t, log.buf.String(), "linked")
}
