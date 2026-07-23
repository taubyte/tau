package schema

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"
)

// A session over the real fixtures validates; a fork can make a breaking edit,
// fail validation in isolation, and be discarded without touching the parent —
// or make a good edit, validate, and merge.
func TestSession(t *testing.T) {
	fixtures := filepath.Join("..", "fixtures", "config")
	ctx := context.Background()

	s, err := NewSession(afero.NewOsFs(), fixtures)
	assert.NilError(t, err)

	// baseline: the whole config validates
	vals, err := s.Validate(ctx, CompileOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(vals) > 0, "expected deferred checks")

	t.Run("fork with a bad edit fails validation, parent untouched", func(t *testing.T) {
		fork, err := s.Fork()
		assert.NilError(t, err)
		assert.NilError(t, fork.Set([]string{"functions", "test_function1_glob"}, []string{"source"}, "not_a_ref"))

		_, err = fork.Validate(ctx, CompileOptions{})
		assert.ErrorContains(t, err, `must be "." or start with "libraries/"`)

		// parent still validates — the bad edit never touched it
		_, err = s.Validate(ctx, CompileOptions{})
		assert.NilError(t, err)
	})

	t.Run("partial validation is compile-free and scoped", func(t *testing.T) {
		fn := []string{"functions", "test_function1_glob"}
		// field-level: enum on trigger.type
		assert.NilError(t, s.ValidateField(fn, []string{"trigger", "type"}, "https"))
		assert.ErrorContains(t, s.ValidateField(fn, []string{"trigger", "type"}, "nope"), "invalid value")

		// resource-level: the fixture function is locally valid...
		assert.Equal(t, len(s.ValidateResource(fn)), 0)
		// ...set a bad enum, and ValidateResource surfaces it (no compile).
		assert.NilError(t, s.Set(fn, []string{"trigger", "type"}, "nope"))
		errs := s.ValidateResource(fn)
		assert.Equal(t, len(errs), 1)
		assert.ErrorContains(t, errs[0], "invalid value")
		// undo
		assert.NilError(t, s.Set(fn, []string{"trigger", "type"}, "http"))
	})

	t.Run("partial validation: unknown fields error, scalar formats are checked", func(t *testing.T) {
		fn := []string{"functions", "test_function1_glob"}
		// (a) an unknown path is reported as unknown, not silently OK
		assert.ErrorContains(t, s.ValidateField(fn, []string{"nonexistent"}, "x"), "unknown field")
		assert.ErrorContains(t, s.ValidateField(fn, []string{"trigger", "typo"}, "x"), "unknown field")
		// a known field with no constraint is still valid
		assert.NilError(t, s.ValidateField(fn, []string{"description"}, "anything"))
		// (b) duration/bytes format is checked per-field, not only at compile
		assert.NilError(t, s.ValidateField(fn, []string{"execution", "timeout"}, "20s"))
		assert.ErrorContains(t, s.ValidateField(fn, []string{"execution", "timeout"}, "20x"), "invalid duration")
		assert.NilError(t, s.ValidateField(fn, []string{"execution", "memory"}, "32GB"))
		assert.ErrorContains(t, s.ValidateField(fn, []string{"execution", "memory"}, "banana"), "invalid size")

		// a legacy Compat alias the accessors accept is recognized too — not
		// "unknown" — and its reference is still checked. "domains" is the compat
		// alias of the canonical "trigger/domains".
		assert.NilError(t, s.ValidateField(fn, []string{"domains"}, []any{"test_domain1"}))
		assert.ErrorContains(t, s.ValidateField(fn, []string{"domains"}, []any{"ghost"}), `no domains named "ghost"`)
		// array-element addressing is still not a field
		assert.ErrorContains(t, s.ValidateField(fn, []string{"tags", "0"}, "x"), "unknown field")
	})

	t.Run("partial validation catches bad references in scope, compile-free", func(t *testing.T) {
		fn := []string{"functions", "test_function1_glob"} // a root function
		// a domain that doesn't exist -> flagged (was silent before)
		assert.ErrorContains(t, s.ValidateField(fn, []string{"trigger", "domains"}, []any{"ghost"}), `no domains named "ghost"`)
		// an existing global domain -> ok
		assert.NilError(t, s.ValidateField(fn, []string{"trigger", "domains"}, []any{"test_domain1"}))
		// a library only defined in test_app1 is out of scope for a root function
		assert.ErrorContains(t, s.ValidateField(fn, []string{"source"}, "libraries/test_library2"), `no libraries named "test_library2"`)
		// "." is a literal, not a reference -> ok
		assert.NilError(t, s.ValidateField(fn, []string{"source"}, "."))

		// resource-level surfaces it too
		assert.NilError(t, s.Set(fn, []string{"trigger", "domains"}, []any{"ghost"}))
		errs := s.ValidateResource(fn)
		assert.Assert(t, len(errs) == 1)
		assert.ErrorContains(t, errs[0], `no domains named "ghost"`)
		assert.NilError(t, s.Set(fn, []string{"trigger", "domains"}, []any{"test_domain1"})) // undo
	})

	complete := func(t *testing.T, res, field []string, partial string) []string {
		t.Helper()
		c, err := s.Complete(res, field, partial)
		assert.NilError(t, err)
		return c
	}

	t.Run("completion: enum members and scoped references, filtered by the partial", func(t *testing.T) {
		fn := []string{"functions", "test_function1_glob"} // a root function

		// enum field — partial filters the members
		all := complete(t, fn, []string{"trigger", "type"}, "")
		assert.Assert(t, slices.Contains(all, "pubsub") && slices.Contains(all, "http"))
		assert.DeepEqual(t, complete(t, fn, []string{"trigger", "type"}, "p"), []string{"pubsub", "p2p"})

		// reference field — the shape literal "." plus in-scope libraries, prefixed.
		// Root scope sees the global library test_library1 (not app1's test_library2).
		src := complete(t, fn, []string{"source"}, "")
		assert.Assert(t, slices.Contains(src, "."), "source offers the inline literal")
		assert.Assert(t, slices.Contains(src, "libraries/test_library1"), "source offers the global library")
		assert.Assert(t, !slices.Contains(src, "libraries/test_library2"), "a root function must not see app1's library")

		// the user's partial narrows it
		assert.DeepEqual(t, complete(t, fn, []string{"source"}, "libraries/test_l"), []string{"libraries/test_library1"})
		assert.DeepEqual(t, complete(t, fn, []string{"source"}, "."), []string{"."})

		// compat alias resolves for completion too, and an unknown path errors
		assert.DeepEqual(t, complete(t, fn, []string{"domains"}, ""), []string{"test_domain1"})
		_, err := s.Complete(fn, []string{"nonexistent"}, "")
		assert.ErrorContains(t, err, "unknown field")
	})

	t.Run("completion: an app function also sees its own app's libraries", func(t *testing.T) {
		appFn := []string{"applications", "test_app1", "functions", "test_function2"}
		src := complete(t, appFn, []string{"source"}, "libraries/")
		assert.Assert(t, slices.Contains(src, "libraries/test_library2"), "app scope sees app1's library")
		assert.Assert(t, slices.Contains(src, "libraries/test_library1"), "and the global one")
	})

	t.Run("fork with a good edit validates and merges", func(t *testing.T) {
		fork, err := s.Fork()
		assert.NilError(t, err)
		assert.NilError(t, fork.Set([]string{"functions", "test_function1_glob"}, []string{"description"}, "edited via fork"))

		_, err = fork.Validate(ctx, CompileOptions{})
		assert.NilError(t, err)
		assert.NilError(t, fork.Merge())

		got, err := s.Get([]string{"functions", "test_function1_glob"}, []string{"description"})
		assert.NilError(t, err)
		assert.Equal(t, got, "edited via fork")
	})
}
