package generic_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/tcc"
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

	// NEW, EDIT AND DELETE, on a kind the vocabulary actually has. The flags
	// come from the schema — a nested `execution/timeout` is flattened to
	// --timeout — so what is passed here is derived rather than remembered.
	assert.NilError(t, run("new", "function", "cli_fn",
		"--description", "made by the generic driver",
		"--timeout", "10s",
		"--memory", "16MB",
		"--call", "ping",
	))

	yaml := read(t, filepath.Join(config, "function", "cli_fn.yaml"))
	for _, want := range []string{"description: made by the generic driver", "timeout: 10s", "call: ping"} {
		assert.Assert(t, strings.Contains(yaml, want), "expected %q in:\n%s", want, yaml)
	}

	// edit: a changed value is written and the document keeps its identity.
	assert.NilError(t, run("edit", "function", "cli_fn", "--call", "pong"))
	yaml = read(t, filepath.Join(config, "function", "cli_fn.yaml"))
	assert.Assert(t, strings.Contains(yaml, "call: pong"), yaml)
	assert.Assert(t, strings.Contains(yaml, "id:"), "the edit kept the id:\n%s", yaml)

	assert.NilError(t, run("delete", "function", "cli_fn"))
	_, err := os.Stat(filepath.Join(config, "function", "cli_fn.yaml"))
	assert.Assert(t, os.IsNotExist(err), "expected the resource file to be gone, got %v", err)
}

// A dynamic key is a BRANCH and not a value: the driver has to create the right
// one and put the fields under it. Whichever kind declares one is the one
// tested, so this survives the branch moving.
func TestDynamicBranch(t *testing.T) {
	root := testutil.WithTCCFixtureCopyEnv(t)

	groups, err := tcc.Groups()
	assert.NilError(t, err)

	for _, g := range groups {
		if g.Container {
			continue
		}
		form, err := tcc.FormFor(g.Def)
		assert.NilError(t, err)

		var sel tcc.Field
		for _, fd := range form.Fields {
			if fd.IsSelector && len(fd.Alternatives) > 1 && len(fd.BranchPrefix) == 0 {
				sel = fd
				break
			}
		}
		if sel.Key == "" {
			continue
		}

		// A BRANCH WITH NOTHING UNDER IT IS NOTHING. The document only grows
		// the key once a field inside it is answered, which is right — an
		// empty branch would be a switch opened onto nothing — so the test has
		// to set one.
		branch := sel.Alternatives[1]

		// A LEAF DIRECTLY UNDER THE BRANCH. A deeper one sits under a nested
		// selector of its own and needs that opened too, which is a different
		// property and belongs to a different test.
		var under tcc.Field
		for _, fd := range form.Fields {
			if fd.IsSelector || len(fd.Path) != 2 || fd.Path[0] != branch {
				continue
			}
			if value(fd) != "" {
				under = fd
				break
			}
		}
		if under.Key == "" {
			continue
		}

		if err := cli.Run("tau", "--defaults", "--yes", "--color", "never",
			"new", g.Name, "cli_branch", "--"+sel.Flag, branch, "--"+under.Flag, value(under)); err != nil {
			continue
		}

		yaml := read(t, filepath.Join(root, "config", g.Dir, "cli_branch.yaml"))
		assert.Assert(t, strings.Contains(yaml, branch+":"),
			"%s: the chosen branch is not a key in the document:\n%s", g.Name, yaml)
		assert.Assert(t, !strings.Contains(yaml, "\n"+sel.Alternatives[0]+":"),
			"%s: the branch that was not chosen is there too:\n%s", g.Name, yaml)
		return
	}

	t.Fatal("no kind in this vocabulary declares a top-level branch")
}

// A bad value is rejected by the DSL's own validator, before anything is
// written.
func TestFieldValidation(t *testing.T) {
	root := testutil.WithTCCFixtureCopyEnv(t)

	err := cli.Run("tau", "--defaults", "--yes", "--color", "never",
		"new", "function", "bad_fn", "--timeout", "20x", "--call", "ping")
	assert.ErrorContains(t, err, "invalid duration")

	_, statErr := os.Stat(filepath.Join(root, "config", "function", "bad_fn.yaml"))
	assert.Assert(t, os.IsNotExist(statErr), "nothing should have been written")
}

// value is something the DSL accepts for a field, or "" when nothing can be
// invented — a reference has to name a resource that exists.
func value(fd tcc.Field) string {
	switch fd.Widget {
	case tcc.WidgetSelect:
		if len(fd.Enum) > 0 {
			return fd.Enum[0]
		}
	case tcc.WidgetScalar:
		if fd.Scalar == "duration" {
			return "30s"
		}
		return "1GB"
	case tcc.WidgetSwitch:
		return "true"
	case tcc.WidgetText:
		return "made_by_a_test"
	}
	return ""
}

// containerKind is whichever kind declares itself a scope.
func containerKind(t *testing.T) tcc.Group {
	t.Helper()

	groups, err := tcc.Groups()
	assert.NilError(t, err)
	for _, g := range groups {
		if g.Container {
			return g
		}
	}

	t.Fatal("this vocabulary declares no container kind")
	return tcc.Group{}
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

	container := containerKind(t)

	assert.NilError(t, run("new", container.Name, "cli_scope", "--description", "scoped"))
	assert.Assert(t, strings.Contains(
		read(t, filepath.Join(config, container.Dir, "cli_scope", "meta.yaml")), "description: scoped"))

	// the new scope is the selected one, so this function is its own
	assert.NilError(t, run("new", "function", "scoped_fn", "--call", "ping"))
	assert.Assert(t, strings.Contains(
		read(t, filepath.Join(config, container.Dir, "cli_scope", "function", "scoped_fn.yaml")), "call: ping"))

	assert.NilError(t, run("select", container.Name, "--none"))
	assert.NilError(t, run("delete", container.Name, "cli_scope"))
	_, err := os.Stat(filepath.Join(config, container.Dir, "cli_scope"))
	assert.Assert(t, os.IsNotExist(err), "expected the application to be gone, got %v", err)
}
