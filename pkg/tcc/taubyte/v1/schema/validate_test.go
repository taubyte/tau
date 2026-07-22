package schema

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"
)

// Validate is the whole-config validator external tools call: it runs the real
// compile pipeline for diagnostics only. A valid project surfaces the deferred
// external checks (DNS, project_id) with no error; an authored-constraint
// violation (bad source shape) comes back as an error, no artifact built.
func TestValidate(t *testing.T) {
	fixtures := filepath.Join("..", "fixtures", "config")

	t.Run("valid returns deferred checks, no error", func(t *testing.T) {
		vals, err := Validate(context.Background(), WithLocal(fixtures))
		assert.NilError(t, err)
		var kinds []string
		for _, v := range vals {
			kinds = append(kinds, v.Validator)
		}
		assert.Assert(t, len(vals) > 0, "expected deferred validations, got none")
		assert.Assert(t, slices.Contains(kinds, "project_id"), "expected a project_id check in %v", kinds)
		assert.Assert(t, slices.Contains(kinds, "dns"), "expected a dns check in %v", kinds)
	})

	t.Run("bad source shape errors", func(t *testing.T) {
		base := afero.NewReadOnlyFs(afero.NewOsFs())
		cow := afero.NewCopyOnWriteFs(base, afero.NewMemMapFs())
		bad := filepath.Join(fixtures, "functions", "test_function1_glob.yaml")
		body := "id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh\nsource: not_a_ref\n"
		assert.NilError(t, afero.WriteFile(cow, bad, []byte(body), 0644))

		_, err := Validate(context.Background(), WithVirtual(cow, fixtures))
		assert.ErrorContains(t, err, `must be "." or start with "libraries/"`)
	})

	// The referential-integrity case: valid shape ("libraries/<name>"), but the
	// library isn't defined. Shape passes at load; the missing ref is caught later
	// at ref-resolution during compile — so Validate rejects it.
	t.Run("source referencing a missing library errors", func(t *testing.T) {
		base := afero.NewReadOnlyFs(afero.NewOsFs())
		cow := afero.NewCopyOnWriteFs(base, afero.NewMemMapFs())
		bad := filepath.Join(fixtures, "functions", "test_function1_glob.yaml")
		body := "id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh\nsource: \"libraries/does_not_exist\"\n"
		assert.NilError(t, afero.WriteFile(cow, bad, []byte(body), 0644))

		_, err := Validate(context.Background(), WithVirtual(cow, fixtures))
		assert.Assert(t, err != nil, "expected an error for a source referencing an undefined library")
		t.Logf("missing-library error: %v", err)
	})

	// Cross-app isolation: a resource in one app cannot reference a resource in a
	// sibling app — only its own app and root/global are in scope. test_library2
	// exists only in test_app1, so a test_app2 function pointing at it must fail.
	// The rule is generic in the resolver (ancestor-only scope walk), not per-kind.
	t.Run("cross-app reference is rejected", func(t *testing.T) {
		base := afero.NewReadOnlyFs(afero.NewOsFs())
		cow := afero.NewCopyOnWriteFs(base, afero.NewMemMapFs())
		f := filepath.Join(fixtures, "applications", "test_app2", "functions", "test_function2.yaml")
		body := "id: QmXuTz6e3W7Y9EJ2hYH4Jk1JAXT7pKnai5NqUWFPVF5Cmx\nsource: \"libraries/test_library2\"\n"
		assert.NilError(t, afero.WriteFile(cow, f, []byte(body), 0644))

		_, err := Validate(context.Background(), WithVirtual(cow, fixtures))
		assert.Assert(t, err != nil, "expected a sibling-app library reference to be rejected")
		t.Logf("cross-app error: %v", err)
	})
}
