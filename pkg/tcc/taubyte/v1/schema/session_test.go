package schema

import (
	"context"
	"path/filepath"
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
