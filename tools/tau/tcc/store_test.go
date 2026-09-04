package tcc_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

// NOTHING HERE NAMES A KIND OR A RESOURCE.
//
// The first version did — `functions`, `test_function1_glob`, `trigger/type`,
// `websites` — against a fixture under pkg/tcc/taubyte/v1 that went with v1 when
// v1 was deleted. Every test in this package broke at once and none of them was
// wrong about the CLI: they were wrong about the vocabulary.
//
// So each test finds what it needs in the fixture the schema itself ships, and
// asserts a property of the store rather than a fact about a fixture.

// open a store over a throwaway copy of the fixture project.
func openStore(t *testing.T) (*tcc.Store, string) {
	t.Helper()
	root := testutil.WithTCCFixtureCopyEnv(t)
	st, err := tcc.Open()
	assert.NilError(t, err)
	return st, root
}

// populated is a kind the fixture actually has resources of, and one of them.
// It skips the container kind, whose instances are directories.
func populated(t *testing.T, st *tcc.Store) (dir, name string) {
	t.Helper()

	groups, err := tcc.Groups()
	assert.NilError(t, err)

	for _, g := range groups {
		if g.Container {
			continue
		}
		names, err := st.List(g.Dir)
		if err != nil || len(names) == 0 {
			continue
		}
		return g.Dir, names[0]
	}

	t.Fatal("the fixture project has no resources of any kind")
	return "", ""
}

// containerGroup is the kind whose instances are scopes.
func containerGroup(t *testing.T) tcc.Group {
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

func TestStoreReadWriteDelete(t *testing.T) {
	st, _ := openStore(t)
	dir, name := populated(t, st)

	// A DOCUMENT DOES NOT CARRY ITS OWN NAME. The file it is filed under is the
	// name, which is why `name` is not an editable field anywhere — it does
	// carry the id the generator minted.
	doc, err := st.Doc(dir, name)
	assert.NilError(t, err)
	assert.Assert(t, tcc.Get(doc, []string{"name"}) == nil)
	assert.Assert(t, tcc.Get(doc, []string{"id"}) != nil, "%s/%s has no id", dir, name)

	// an absent group is simply empty, not an error
	empty, err := st.List("nonexistent")
	assert.NilError(t, err)
	assert.Equal(t, len(empty), 0)

	id, err := st.ProjectID()
	assert.NilError(t, err)
	assert.Assert(t, id != "")

	// write a new resource, read it back, delete it
	assert.NilError(t, st.Write(dir, "made", tcc.Doc{"id": "QmNew", "description": "made here"}))

	back, err := st.Doc(dir, "made")
	assert.NilError(t, err)
	assert.Equal(t, tcc.Get(back, []string{"description"}), "made here")

	assert.NilError(t, st.Delete(dir, "made"))
	after, _ := st.List(dir)
	assert.Assert(t, !contains(after, "made"))
}

// TestWriteIsAMinimalDiff: a value dropped from the doc is deleted from the
// file, so an edit does not rewrite what it did not touch.
func TestStoreWriteDiff(t *testing.T) {
	st, _ := openStore(t)
	dir, name := populated(t, st)

	doc, err := st.Doc(dir, name)
	assert.NilError(t, err)

	tcc.Set(doc, []string{"description"}, "changed")
	assert.NilError(t, st.Write(dir, name, doc))

	back, err := st.Doc(dir, name)
	assert.NilError(t, err)
	assert.Equal(t, tcc.Get(back, []string{"description"}), "changed")
	assert.Assert(t, tcc.Get(back, []string{"id"}) != nil, "and it did not lose what it did not touch")

	tcc.Set(doc, []string{"description"}, nil)
	assert.NilError(t, st.Write(dir, name, doc))

	back, _ = st.Doc(dir, name)
	assert.Assert(t, tcc.Get(back, []string{"description"}) == nil)
}

// TestValidationAndCompletionComeFromTheDSL, for whichever field the vocabulary
// happens to constrain.
func TestStoreValidateAndComplete(t *testing.T) {
	st, _ := openStore(t)
	dir, name := populated(t, st)

	form, err := tcc.FormFor(groupFor(t, dir).Def)
	assert.NilError(t, err)

	var enum tcc.Field
	for _, fd := range form.Fields {
		if fd.Widget == tcc.WidgetSelect && len(fd.Enum) > 0 {
			enum = fd
			break
		}
	}
	if enum.Key == "" {
		t.Skipf("%s constrains no field by a set of values", dir)
	}

	assert.NilError(t, st.ValidateField(dir, name, enum.Path, enum.Enum[0]))
	assert.ErrorContains(t,
		st.ValidateField(dir, name, enum.Path, "a-value-the-dsl-does-not-allow"), "invalid value")

	// COMPLETION IS THE ENUM AND NOTHING ELSE. A field whose members are
	// declared has exactly them; offering more would offer values the compiler
	// refuses.
	got := st.Complete(dir, name, enum.Path)
	assert.DeepEqual(t, sorted(got), sorted(enum.Enum))
}

func TestStoreProjectRoot(t *testing.T) {
	st, _ := openStore(t)

	// project root fields are the same DSL, one level up
	assert.NilError(t, st.SetProject(map[string]any{"description": "root edit"}))

	// and the inventory reads without error whether or not this vocabulary has
	// a repo-backed kind.
	_, err := st.RepositoryNames()
	assert.NilError(t, err)
}

// TestAContainerIsAScope. A container instance is a directory, and it is the
// scope everything else is read and written under once selected.
func TestStoreContainerScope(t *testing.T) {
	st, root := openStore(t)
	container := containerGroup(t)

	scopes, err := st.List(container.Dir)
	assert.NilError(t, err)
	assert.Assert(t, len(scopes) > 0, "the fixture declares no %s", container.Dir)
	scope := scopes[0]

	assert.NilError(t, session.Set().SelectedApplication(scope))
	scoped, err := tcc.Open()
	assert.NilError(t, err)
	assert.Equal(t, scoped.Application(), scope)

	// A CONTAINER IS NEVER SCOPED TO ITSELF: it stays listable at project scope
	// while you are selected inside one, or there would be no way back out.
	stillThere, err := scoped.List(container.Dir)
	assert.NilError(t, err)
	assert.Assert(t, contains(stillThere, scope))

	// A write inside the scope lands inside the scope's directory.
	dir, _ := populated(t, st)
	assert.NilError(t, scoped.Write(dir, "scoped_one", tcc.Doc{"id": "QmS", "description": "inside"}))

	_, statErr := os.Stat(filepath.Join(root, "config", container.Dir, scope, dir, "scoped_one.yaml"))
	assert.NilError(t, statErr)

	// And it is not visible from the project scope.
	outer, err := st.List(dir)
	assert.NilError(t, err)
	assert.Assert(t, !contains(outer, "scoped_one"))
}

func TestGroupForAndRepositoryName(t *testing.T) {
	groups, err := tcc.Groups()
	assert.NilError(t, err)

	for _, g := range groups {
		found, err := tcc.GroupFor(g.Dir)
		assert.NilError(t, err, g.Dir)
		assert.Equal(t, found.Name, g.Name)

		// A kind with no repository shape says so rather than answering with an
		// empty name, which a caller would write into a config.
		form, err := tcc.FormFor(g.Def)
		assert.NilError(t, err)
		if form.Repo() == nil {
			_, err := tcc.RepositoryName(g.Dir, tcc.Doc{})
			assert.ErrorContains(t, err, "not backed by a repository", g.Dir)
		}
	}

	_, err = tcc.GroupFor("nonesuch")
	assert.ErrorContains(t, err, "no resource kind")
}

func TestConfigDir(t *testing.T) {
	assert.Equal(t, tcc.ConfigDir("/x/proj"), filepath.Join("/x/proj", "config"))
}

func groupFor(t *testing.T, dir string) tcc.Group {
	t.Helper()
	g, err := tcc.GroupFor(dir)
	assert.NilError(t, err)
	return g
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
