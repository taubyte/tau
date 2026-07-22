package session

import (
	"errors"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"gotest.tools/v3/assert"
)

func noCompiler(afero.Fs, string, string) (*interp.Compiler, error) {
	return nil, errors.New("compiler not needed in this test")
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// A fork's edits (set / new resource / delete) stay isolated until Merge, then
// collapse onto the parent — writes and deletes both.
func TestSessionForkMerge(t *testing.T) {
	src := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(src, "/functions/foo.yaml", []byte("id: fooid\n"), 0o644))
	assert.NilError(t, afero.WriteFile(src, "/functions/bar.yaml", []byte("id: barid\n"), 0o644))

	parent, err := New(src, "/", noCompiler)
	assert.NilError(t, err)

	fork, err := parent.Fork()
	assert.NilError(t, err)

	// edit an existing resource, delete one, create a new one — all on the fork
	assert.NilError(t, fork.Set([]string{"functions", "foo"}, []string{"description"}, "hello"))
	assert.NilError(t, fork.Delete([]string{"functions", "bar"}, nil))
	assert.NilError(t, fork.Set([]string{"functions", "baz"}, []string{"id"}, "bazid"))

	// parent is untouched before merge
	names, err := parent.List([]string{"functions"})
	assert.NilError(t, err)
	assert.Assert(t, has(names, "bar"), "parent still has bar before merge")
	assert.Assert(t, !has(names, "baz"), "parent must not see fork's new baz before merge")
	pv, _ := parent.Get([]string{"functions", "foo"}, []string{"description"})
	assert.Assert(t, pv == nil, "parent foo.description unset before merge")

	// merge collapses the changeset onto the parent
	assert.NilError(t, fork.Merge())

	names, err = parent.List([]string{"functions"})
	assert.NilError(t, err)
	assert.Assert(t, !has(names, "bar"), "bar deleted in fork -> gone from parent after merge")
	assert.Assert(t, has(names, "baz"), "baz created in fork -> present in parent after merge")
	assert.Assert(t, has(names, "foo"), "foo still present")

	got, err := parent.Get([]string{"functions", "foo"}, []string{"description"})
	assert.NilError(t, err)
	assert.Equal(t, got, "hello")
}
