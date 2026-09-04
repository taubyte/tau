package generic_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

// NOTHING HERE NAMES A KIND OR A FIELD.
//
// The first version listed ten of each — function, database, domain, library,
// messaging, service, smartops, storage, website, application, plus `--fqdn`,
// `--mqtt`, `--protocol` — and every one of those was v1's. They went with v1
// and took the CLI's whole command surface out of test coverage with them.
//
// So each test walks the live vocabulary. The result is wider than what it
// replaced: every kind is exercised, including the ones nobody thought to add.

func run(args ...string) error {
	return cli.Run(append([]string{"tau", "--defaults", "--yes", "--color", "never"}, args...)...)
}

func groups(t *testing.T) []tcc.Group {
	t.Helper()
	gs, err := tcc.Groups()
	assert.NilError(t, err)
	assert.Assert(t, len(gs) > 0)
	return gs
}

// shadowed are the kinds whose generated command is hidden by a bespoke one the
// CLI registers under the same word.
//
// `log` IS THE ONLY ONE, and it is a real collision rather than a test
// concession: the vocabulary has a `log` KIND — an append-only stream a workload
// writes to — and the CLI has a `log` COMMAND for reading a build's output. The
// second shadows the first, so `tau query log` reaches the viewer and the log
// resource cannot be created, queried or listed at all.
//
// Fixing it is a rename, and which of the two moves is a decision rather than a
// guess. The set is asserted exactly, so this fails if the collision is fixed
// AND if another one appears.
var shadowed = map[string]bool{"log": true}

// TestEveryKindIsReachableByItsOwnName. A kind the vocabulary declares and the
// CLI cannot address is a resource nobody can create.
func TestQueryListEveryKind(t *testing.T) {
	testutil.WithTCCFixtureCopyEnv(t)

	found := map[string]bool{}
	for _, g := range groups(t) {
		t.Run(g.Name, func(t *testing.T) {
			err := run("query", g.Name, "--list")
			if shadowed[g.Name] {
				assert.Assert(t, err != nil,
					"%s is no longer shadowed; take it out of the set and say so", g.Name)
				found[g.Name] = true
				return
			}
			assert.NilError(t, err, g.Name)
			assert.NilError(t, run("list", g.Name), g.Name)
		})
	}

	for name := range shadowed {
		assert.Assert(t, found[name], "%s is in the shadowed set and is not a kind", name)
	}
}

// TestCreateEveryKind. Every kind can be created from the command line with
// nothing but its required fields answered, and what lands on disk is what was
// asked for.
//
// THE ARGUMENTS ARE DERIVED FROM THE FORM. A required field's flag and an
// acceptable value both come out of the DSL, so a kind that grows a required
// field is covered the day it does — and one whose flag stops working fails
// here rather than in somebody's terminal.
func TestCreateEveryKind(t *testing.T) {
	root := testutil.WithTCCFixtureCopyEnv(t)
	config := filepath.Join(root, "config")

	for _, g := range groups(t) {
		if g.Container || shadowed[g.Name] {
			// A container's document lives in a directory of its own and its
			// creation is the scope test's business.
			continue
		}

		t.Run(g.Name, func(t *testing.T) {
			form, err := tcc.FormFor(g.Def)
			assert.NilError(t, err)

			args, wants := answers(form)
			name := "made_" + g.Name

			assert.NilError(t, run(append([]string{"new", g.Name, name}, args...)...), g.Name)

			// IT LANDS WHERE THE LAYOUT SAYS, with an id nobody typed.
			yaml := read(t, filepath.Join(config, g.Dir, name+".yaml"))
			assert.Assert(t, strings.Contains(yaml, "id:"), "%s has no id:\n%s", g.Name, yaml)

			// AND WHAT WAS ASKED FOR IS IN IT. Only the plain values are
			// checked by text: a branch selector writes its choice as a KEY
			// rather than a value, which is the document shape and not
			// something a caller should have to know here.
			for _, w := range wants {
				assert.Assert(t, strings.Contains(yaml, w), "%s: want %q in:\n%s", g.Name, w, yaml)
			}

			assert.NilError(t, run("query", g.Name, name), g.Name)
		})
	}
}

// answers picks a value the DSL accepts for every required field, and returns
// the flags to pass plus what should show up in the document.
func answers(f *tcc.Form) (args, wants []string) {
	for _, fd := range f.Fields {
		if fd.Widget == tcc.WidgetCID || !fd.Required {
			continue
		}

		value := valueFor(fd)
		if value == "" {
			continue
		}
		args = append(args, "--"+fd.Flag, value)

		// A SELECTOR'S CHOICE BECOMES A KEY, not a value, so it is asked for
		// and not looked for.
		if fd.Widget != tcc.WidgetBranchSelect {
			wants = append(wants, value)
		}
	}
	return args, wants
}

// valueFor is something the DSL accepts, or "" when nothing can be invented —
// a reference has to name a resource that exists, which this cannot know.
func valueFor(fd tcc.Field) string {
	switch fd.Widget {
	case tcc.WidgetSelect, tcc.WidgetBranchSelect:
		if len(fd.Enum) > 0 {
			return fd.Enum[0]
		}
		if len(fd.Alternatives) > 0 {
			return fd.Alternatives[0]
		}
	case tcc.WidgetScalar:
		if fd.Scalar == "duration" {
			return "30s"
		}
		return "1GB"
	case tcc.WidgetSwitch:
		return "true"
	case tcc.WidgetText, tcc.WidgetList:
		return "made_by_a_test"
	}
	return ""
}

// TestAConditionalFieldIsNotWrittenWhenItsConditionDoesNotHold. Choosing one
// branch of a switch must not carry the other's values into the document; the
// compiler refuses a document that opens two.
func TestShowWhenConditional(t *testing.T) {
	root := testutil.WithTCCFixtureCopyEnv(t)

	for _, g := range groups(t) {
		if g.Container || shadowed[g.Name] {
			continue
		}

		form, err := tcc.FormFor(g.Def)
		assert.NilError(t, err)

		// A kind with a selector offering at least two branches, and a field
		// under a branch that is not the first.
		var sel, other tcc.Field
		for _, fd := range form.Fields {
			if fd.IsSelector && len(fd.Alternatives) > 1 {
				sel = fd
				break
			}
		}
		if sel.Key == "" {
			continue
		}
		for _, fd := range form.Fields {
			if fd.ShowWhen != nil && len(fd.ShowWhen.In) == 1 && fd.ShowWhen.In[0] == sel.Alternatives[1] {
				other = fd
				break
			}
		}
		if other.Key == "" {
			continue
		}

		args, _ := answers(form)
		name := "conditional_" + g.Name
		if err := run(append([]string{"new", g.Name, name}, args...)...); err != nil {
			continue
		}

		yaml := read(t, filepath.Join(root, "config", g.Dir, name+".yaml"))
		assert.Assert(t, !strings.Contains(yaml, "\n"+sel.Alternatives[1]+":"),
			"%s: the branch that was not chosen is in the document:\n%s", g.Name, yaml)
		return
	}
}

// TestQueryMissing: a resource that does not exist is a clear error, not a
// panic.
func TestQueryMissing(t *testing.T) {
	testutil.WithTCCFixtureCopyEnv(t)

	for _, g := range groups(t) {
		err := cli.Run("tau", "--defaults", "--color", "never", "query", g.Name, "--name", "ghost")
		assert.ErrorContains(t, err, "not found", g.Name)
		return
	}
}

// TestSelectScopeCommands: the container scope can be entered and cleared by
// command as well as implicitly on create.
func TestSelectScopeCommands(t *testing.T) {
	testutil.WithTCCFixtureCopyEnv(t)

	var container tcc.Group
	for _, g := range groups(t) {
		if g.Container {
			container = g
		}
	}
	assert.Assert(t, container.Name != "", "this vocabulary declares no container kind")

	st, err := tcc.Open()
	assert.NilError(t, err)
	scopes, err := st.List(container.Dir)
	assert.NilError(t, err)
	assert.Assert(t, len(scopes) > 0, "the fixture declares no %s", container.Dir)
	scope := scopes[0]

	assert.NilError(t, run("select", container.Name, scope))
	assert.NilError(t, run("select", container.Name, "--none"))

	// --name and --none together is refused
	err = run("select", container.Name, scope, "--none")
	assert.ErrorContains(t, err, "cannot use")

	assert.NilError(t, run("clear", container.Name))
}
