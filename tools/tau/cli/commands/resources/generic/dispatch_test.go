package generic

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/tcc"
	"gotest.tools/v3/assert"
)

func linkFor(t *testing.T, dir string) link {
	t.Helper()
	g, err := tcc.GroupFor(dir)
	assert.NilError(t, err)
	bind, err := New(g)
	assert.NilError(t, err)
	return bind().(link)
}

// Which verbs a kind exposes follows its shape: a repo-backed kind gets the git
// verbs, a code-backed kind gets run, a container gets select/clear, and a plain
// kind gets none of those.
func TestVerbsByShape(t *testing.T) {
	fn := linkFor(t, "functions")     // code-backed, not repo, not container
	web := linkFor(t, "websites")     // repo-backed
	db := linkFor(t, "databases")     // plain
	app := linkFor(t, "applications") // container

	// git verbs: repo-backed only
	for _, l := range []link{fn, db, app} {
		assert.Assert(t, l.Clone() == common.NotImplemented)
		assert.Assert(t, l.Push() == common.NotImplemented)
		assert.Assert(t, l.Pull() == common.NotImplemented)
		assert.Assert(t, l.Checkout() == common.NotImplemented)
		assert.Assert(t, l.Import() == common.NotImplemented)
	}
	assert.Assert(t, web.Clone() != common.NotImplemented)
	assert.Assert(t, web.Push() != common.NotImplemented)
	assert.Assert(t, web.Import() != common.NotImplemented)

	// run: code-backed only
	assert.Assert(t, fn.Run() != common.NotImplemented)
	assert.Assert(t, web.Run() == common.NotImplemented)
	assert.Assert(t, db.Run() == common.NotImplemented)

	// select/clear: container only
	assert.Assert(t, app.Select() != common.NotImplemented)
	assert.Assert(t, app.Clear() != common.NotImplemented)
	assert.Assert(t, fn.Select() == common.NotImplemented)
	assert.Assert(t, db.Clear() == common.NotImplemented)

	// new/edit/query/list/delete always present
	for _, l := range []link{fn, web, db, app} {
		assert.Assert(t, l.New() != nil)
		assert.Assert(t, l.Edit() != nil)
		assert.Assert(t, l.Query() != nil)
		assert.Assert(t, l.List() != nil)
		assert.Assert(t, l.Delete() != nil)
	}
}

// The base command carries the DSL name, the plural dir alias, and the CLI's
// established shorthand.
func TestBaseAliases(t *testing.T) {
	cmd, _ := linkFor(t, "applications").Base()
	assert.Equal(t, cmd.Name, "application")
	assert.Assert(t, contains(cmd.Aliases, "applications"))
	assert.Assert(t, contains(cmd.Aliases, "app"))
}

// A repo-backed kind's flags include the repository flow's inputs and drop the
// per-field repository entries; a code-backed kind adds the template flags.
func TestFlagsByShape(t *testing.T) {
	names := func(l link) map[string]bool {
		m := map[string]bool{}
		for _, f := range l.flagsFor() {
			m[f.Names()[0]] = true
		}
		return m
	}
	web := names(linkFor(t, "websites"))
	assert.Assert(t, web["repository-name"])
	assert.Assert(t, web["generate-repository"])
	assert.Assert(t, !web["fullname"]) // repository block isn't typed field-by-field

	fn := names(linkFor(t, "functions"))
	assert.Assert(t, fn["template"])
	assert.Assert(t, fn["type"])
	assert.Assert(t, !fn["repository-name"])
}

// The repo adapter reads and writes the repository block through the DSL shape.
func TestRepoResourceAdapter(t *testing.T) {
	web := linkFor(t, "websites")
	r := &resource{
		l:     web,
		shape: web.repo,
		name:  "site",
		doc: tcc.Doc{
			"description": "d",
			"source": map[string]any{
				"branch": "main",
				"github": map[string]any{"fullname": "u/r", "id": "9"},
			},
		},
	}
	g := r.Get()
	assert.Equal(t, g.Name(), "site")
	assert.Equal(t, g.Description(), "d")
	assert.Equal(t, g.RepoName(), "u/r")
	assert.Equal(t, g.RepoID(), "9")
	assert.Equal(t, g.Branch(), "main")
	assert.Assert(t, len(g.RepositoryURL()) > 0)

	r.Set().RepoID("42")
	r.Set().RepoName("u/other")
	assert.Equal(t, r.Get().RepoID(), "42")
	assert.Equal(t, r.Get().RepoName(), "u/other")
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
