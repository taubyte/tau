//go:build docker_integration

package artifact_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/build/artifact"
)

// needsEgressRestriction skips when this machine cannot confine a container's
// network.
//
// IT IS AN ENVIRONMENT CAPABILITY AND NOT A CHOICE. A build runs a stranger's
// commands, so pkg/containers installs a firewall and REFUSES TO START THE
// CONTAINER if it cannot — a rootless docker keeps its bridge in its own
// namespace, so tau's netguard bridge is not there to find. That fail-closed
// behaviour is the property; a test that turned the restriction off to get
// green would be testing a build this service will never run.
func needsEgressRestriction(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "tau-netguard0") {
		t.Skip("this machine cannot restrict a container's egress (rootless docker); a build will not run here")
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
		Commands: []string{"false", "echo this must not run > out"},
		Output:   "out",
	}, &log)
	needsEgressRestriction(t, err)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "left nothing at",
		"it failed at the command, not at the missing output")
	require.NotContains(t, log.String(), "this must not run")
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
