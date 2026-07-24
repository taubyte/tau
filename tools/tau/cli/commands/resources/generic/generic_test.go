package generic_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

// The whole resource surface is derived from the DSL, so one kind exercised
// end-to-end (create with flags, read back, edit a dynamic branch, delete)
// covers the machinery every kind shares.
func TestResourceLifecycle(t *testing.T) {
	root := testutil.WithTCCFixtureCopyEnv(t)
	config := filepath.Join(root, "config")

	run := func(args ...string) error {
		return cli.Run(append([]string{"tau", "--defaults", "--yes", "--color", "never"}, args...)...)
	}

	// new: flags come from the schema — trigger.type, trigger.method and
	// execution.timeout are nested fields flattened to --type/--method/--timeout.
	assert.NilError(t, run("new", "function", "cli_fn",
		"--description", "made by the generic driver",
		"--type", "https",
		"--method", "POST",
		"--paths", "/gen",
		"--timeout", "10s",
		"--memory", "16MB",
		"--call", "ping",
		"--source", ".",
	))

	yaml := read(t, filepath.Join(config, "functions", "cli_fn.yaml"))
	for _, want := range []string{"type: https", "method: POST", "/gen", "timeout: 10s", "call: ping"} {
		assert.Assert(t, strings.Contains(yaml, want), "expected %q in:\n%s", want, yaml)
	}

	// edit: switching the trigger writes the new field and the document keeps
	// its identity.
	assert.NilError(t, run("edit", "function", "cli_fn", "--type", "pubsub", "--channel", "chan1"))
	yaml = read(t, filepath.Join(config, "functions", "cli_fn.yaml"))
	assert.Assert(t, strings.Contains(yaml, "type: pubsub"), yaml)
	assert.Assert(t, strings.Contains(yaml, "channel: chan1"), yaml)

	assert.NilError(t, run("delete", "function", "cli_fn"))
	_, err := os.Stat(filepath.Join(config, "functions", "cli_fn.yaml"))
	assert.Assert(t, os.IsNotExist(err), "expected the resource file to be gone, got %v", err)
}

// A storage's `type` is a dynamic key in the DSL ({object|streaming}), not a
// value — the driver has to create the right branch and put size under it.
func TestDynamicBranch(t *testing.T) {
	root := testutil.WithTCCFixtureCopyEnv(t)

	assert.NilError(t, cli.Run("tau", "--defaults", "--yes", "--color", "never",
		"new", "storage", "cli_store", "--type", "streaming", "--size", "1GB", "--match", "/s"))

	yaml := read(t, filepath.Join(root, "config", "storages", "cli_store.yaml"))
	assert.Assert(t, strings.Contains(yaml, "streaming:"), yaml)
	assert.Assert(t, strings.Contains(yaml, "size: 1GB"), yaml)
}

// A bad value is rejected by the DSL's own validator, before anything is
// written.
func TestFieldValidation(t *testing.T) {
	root := testutil.WithTCCFixtureCopyEnv(t)

	err := cli.Run("tau", "--defaults", "--yes", "--color", "never",
		"new", "function", "bad_fn", "--type", "https", "--timeout", "20x", "--call", "ping")
	assert.ErrorContains(t, err, "invalid duration")

	_, statErr := os.Stat(filepath.Join(root, "config", "functions", "bad_fn.yaml"))
	assert.Assert(t, os.IsNotExist(statErr), "nothing should have been written")
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	assert.NilError(t, err)
	return string(b)
}

// An application is a resource kind like any other in the DSL — it just holds
// resources of its own, which makes it a scope. Creating one writes its config
// document and enters it; resources created next land inside it.
func TestContainerScope(t *testing.T) {
	root := testutil.WithTCCFixtureCopyEnv(t)
	config := filepath.Join(root, "config")

	run := func(args ...string) error {
		return cli.Run(append([]string{"tau", "--defaults", "--yes", "--color", "never"}, args...)...)
	}

	assert.NilError(t, run("new", "application", "cli_app", "--description", "scoped"))
	assert.Assert(t, strings.Contains(
		read(t, filepath.Join(config, "applications", "cli_app", "config.yaml")), "description: scoped"))

	// the new application is the selected scope, so this function is its own
	assert.NilError(t, run("new", "function", "scoped_fn", "--type", "https", "--call", "ping"))
	assert.Assert(t, strings.Contains(
		read(t, filepath.Join(config, "applications", "cli_app", "functions", "scoped_fn.yaml")), "call: ping"))

	assert.NilError(t, run("select", "application", "--none"))
	assert.NilError(t, run("delete", "application", "cli_app"))
	_, err := os.Stat(filepath.Join(config, "applications", "cli_app"))
	assert.Assert(t, os.IsNotExist(err), "expected the application to be gone, got %v", err)
}
