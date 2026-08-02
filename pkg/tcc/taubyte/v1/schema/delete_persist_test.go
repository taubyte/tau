package schema

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"
)

// Removing a field has to reach the FILE, through every route a consumer takes.
// It did not: a deletion updated the in-memory document but never marked it
// dirty, and Sync writes only dirty documents — so the key survived on disk and
// came back on reload. Five of the seven routes below were broken; the two that
// worked did so by accident (one masked by a simultaneous set marking the same
// document, one going through file removal rather than the YAML tree), which is
// why nothing noticed.
//
// Every assertion here reads the serialized document, never the in-memory view:
// reading through Get always saw the deletion and is blind to this entire class.
func TestFieldDeletionReachesTheDocument(t *testing.T) {
	fixtures := filepath.Join("..", "fixtures", "config")
	res := []string{"databases", "test_database1"}

	open := func(t *testing.T) *Session {
		t.Helper()
		s, err := NewSession(afero.NewOsFs(), fixtures)
		assert.NilError(t, err)
		return s
	}
	assertGone := func(t *testing.T, s *Session, key string) {
		t.Helper()
		_, data, err := s.Serialize(res)
		assert.NilError(t, err)
		assert.Assert(t, !strings.Contains(string(data), key),
			"%q must be gone from the serialized document:\n%s", key, data)
	}
	// the whole document, minus one key — what an editor hands back
	without := func(t *testing.T, s *Session, path ...string) map[string]any {
		t.Helper()
		v, err := s.Get(res, nil)
		assert.NilError(t, err)
		m := v.(map[string]any)
		parent := m
		for _, seg := range path[:len(path)-1] {
			parent = parent[seg].(map[string]any)
		}
		delete(parent, path[len(path)-1])
		return m
	}

	t.Run("a direct delete", func(t *testing.T) {
		s := open(t)
		assert.NilError(t, s.Delete(res, []string{"description"}))
		assertGone(t, s, "description")
	})

	t.Run("through a fork and merge", func(t *testing.T) {
		s := open(t)
		fork, err := s.Fork()
		assert.NilError(t, err)
		assert.NilError(t, fork.Delete(res, []string{"description"}))
		assert.NilError(t, fork.Merge())
		assertGone(t, s, "description")
	})

	t.Run("a whole-document diff that only removes", func(t *testing.T) {
		// The isolating case. With any other edit to the same document the
		// removal rides along on that edit's dirty mark and looks fine.
		s := open(t)
		assert.NilError(t, s.SetResource(res, without(t, s, "description")))
		assertGone(t, s, "description")
	})

	t.Run("a nested key, removed on its own", func(t *testing.T) {
		s := open(t)
		assert.NilError(t, s.SetResource(res, without(t, s, "storage", "size")))
		assertGone(t, s, "size")
	})

	t.Run("and it survives a save and reopen", func(t *testing.T) {
		s := open(t)
		assert.NilError(t, s.Delete(res, []string{"description"}))
		out := afero.NewMemMapFs()
		assert.NilError(t, s.Save(out, "/"))

		reopened, err := NewSession(out, "/")
		assert.NilError(t, err)
		_, err = reopened.Get(res, []string{"description"})
		assert.Assert(t, err != nil, "the deletion must survive the round trip through disk")
	})

	t.Run("removing the resource itself still works", func(t *testing.T) {
		s := open(t)
		assert.NilError(t, s.Delete([]string{"functions", "test_function2_glob"}, nil))
		names, err := s.Names("functions", "")
		assert.NilError(t, err)
		for _, n := range names {
			assert.Assert(t, n != "test_function2_glob", "the resource's file must be gone")
		}
	})
}
