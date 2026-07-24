package tcc_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

// open a store over a throwaway copy of the fixture project.
func openStore(t *testing.T) (*tcc.Store, string) {
	t.Helper()
	root := testutil.WithTCCFixtureCopyEnv(t)
	st, err := tcc.Open()
	assert.NilError(t, err)
	return st, root
}

func TestStoreReadWriteDelete(t *testing.T) {
	st, _ := openStore(t)

	// list + read an existing resource
	names, err := st.List("functions")
	assert.NilError(t, err)
	assert.Assert(t, len(names) > 0)

	doc, err := st.Doc("functions", "test_function1_glob")
	assert.NilError(t, err)
	assert.Equal(t, tcc.Get(doc, []string{"trigger", "type"}), "http")

	// an absent group is simply empty, not an error
	empty, err := st.List("nonexistent")
	assert.NilError(t, err)
	assert.Equal(t, len(empty), 0)

	// write a new resource, read it back, delete it
	id, err := st.ProjectID()
	assert.NilError(t, err)
	assert.Assert(t, id != "")

	nd := tcc.Doc{"id": "QmNew", "trigger": map[string]any{"type": "https"}}
	assert.NilError(t, st.Write("functions", "made", nd))
	back, err := st.Doc("functions", "made")
	assert.NilError(t, err)
	assert.Equal(t, tcc.Get(back, []string{"trigger", "type"}), "https")

	assert.NilError(t, st.Delete("functions", "made"))
	names2, _ := st.List("functions")
	assert.Assert(t, !contains(names2, "made"))
}

// Write is a minimal diff: a value dropped from the doc is deleted from the file.
func TestStoreWriteDiff(t *testing.T) {
	st, _ := openStore(t)
	res := "test_function1_glob"

	doc, err := st.Doc("functions", res)
	assert.NilError(t, err)
	tcc.Set(doc, []string{"description"}, "changed")
	tcc.Set(doc, []string{"trigger", "method"}, nil) // drop it
	assert.NilError(t, st.Write("functions", res, doc))

	back, _ := st.Doc("functions", res)
	assert.Equal(t, tcc.Get(back, []string{"description"}), "changed")
	assert.Assert(t, tcc.Get(back, []string{"trigger", "method"}) == nil)
}

func TestStoreValidateAndComplete(t *testing.T) {
	st, _ := openStore(t)
	res := "test_function1_glob"

	// enum validator
	assert.NilError(t, st.ValidateField("functions", res, []string{"trigger", "type"}, "https"))
	assert.ErrorContains(t, st.ValidateField("functions", res, []string{"trigger", "type"}, "nope"), "invalid value")

	// completion: enum members, and a reference field lists in-scope resources
	got := st.Complete("functions", res, []string{"trigger", "type"})
	sort.Strings(got)
	assert.DeepEqual(t, got, []string{"http", "https", "p2p", "pubsub"})

	domains := st.Complete("functions", res, []string{"trigger", "domains"})
	assert.Assert(t, contains(domains, "test_domain1"))
}

func TestStoreProjectRootAndRepos(t *testing.T) {
	st, _ := openStore(t)

	// project root fields are the same DSL, one level up
	assert.NilError(t, st.SetProject(map[string]any{
		"description":        "root edit",
		"clouds/foo.io/plan": "pro",
	}))

	// repository inventory across scopes, by shape (websites + libraries carry repos)
	repos, err := st.RepositoryNames()
	assert.NilError(t, err)
	assert.Assert(t, contains(repos, "taubyte-test/photo_booth"))
	assert.Assert(t, contains(repos, "taubyte-test/library1"))
}

// A container instance is a directory with a config document, and it is the
// scope everything else is read/written under once selected.
func TestStoreContainerScope(t *testing.T) {
	st, root := openStore(t)

	// project scope: application config lives in its directory
	app, err := st.Doc("applications", "test_app1")
	assert.NilError(t, err)
	assert.Equal(t, tcc.Get(app, []string{"description"}), "this is test app 1")

	// enter the application: resource reads are now scoped to it
	assert.NilError(t, session.Set().SelectedApplication("test_app1"))
	scoped, err := tcc.Open()
	assert.NilError(t, err)
	assert.Equal(t, scoped.Application(), "test_app1")

	names, err := scoped.List("functions")
	assert.NilError(t, err)
	assert.Assert(t, contains(names, "test_function2")) // the app's own function
	assert.Assert(t, !contains(names, "test_function1_glob"))

	// the scoped file really lands inside the application directory
	assert.NilError(t, scoped.Write("functions", "scoped_fn", tcc.Doc{"id": "QmS", "trigger": map[string]any{"type": "https"}}))
	_, statErr := stat(filepath.Join(root, "config", "applications", "test_app1", "functions", "scoped_fn.yaml"))
	assert.NilError(t, statErr)
}

// A value authored at a legacy Compat path reads at its canonical path, and a
// rewrite drops the stale key.
func TestStoreCompatAliasResolves(t *testing.T) {
	st, _ := openStore(t)
	res := "compat_fn"

	// author "domains" at the legacy top-level path (canonical is trigger/domains)
	assert.NilError(t, st.Session().Set([]string{"functions", res}, []string{"id"}, "QmC"))
	assert.NilError(t, st.Session().Set([]string{"functions", res}, []string{"domains"}, []string{"test_domain1"}))
	assert.NilError(t, st.Session().Sync())

	doc, err := st.Doc("functions", res)
	assert.NilError(t, err)
	// Read resolves the legacy value onto the canonical path
	assert.DeepEqual(t, strs(tcc.Get(doc, []string{"trigger", "domains"})), []string{"test_domain1"})
}

func TestGroupForAndRepositoryName(t *testing.T) {
	g, err := tcc.GroupFor("websites")
	assert.NilError(t, err)
	assert.Equal(t, g.Name, "website")

	doc := tcc.Doc{"source": map[string]any{"github": map[string]any{"fullname": "u/r"}}}
	name, err := tcc.RepositoryName("websites", doc)
	assert.NilError(t, err)
	assert.Equal(t, name, "u/r")

	// a non-repo kind reports so
	_, err = tcc.RepositoryName("functions", tcc.Doc{})
	assert.ErrorContains(t, err, "not backed by a repository")
}

func TestConfigDir(t *testing.T) {
	assert.Equal(t, tcc.ConfigDir("/x/proj"), filepath.Join("/x/proj", "config"))
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func strs(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, len(t))
		for i, e := range t {
			out[i], _ = e.(string)
		}
		return out
	}
	return nil
}

func stat(p string) (os.FileInfo, error) { return os.Stat(p) }
