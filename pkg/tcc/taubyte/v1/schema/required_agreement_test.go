package schema

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

// The point of partial validation is that an editor can trust it: what it calls
// clean, the compiler must accept, and what it flags as missing, the compiler
// must reject. Those are two independent implementations of required-ness (the
// load path in engine, the value-driven path in session), so this drives every
// required field of every kind through both and demands they agree.
//
// Data-driven off the DSL — a field that gains or loses Required()/RequiredWhen
// is covered automatically, and a kind added later cannot slip past.
func TestRequiredCheckAgreesWithTheCompiler(t *testing.T) {
	fixtures := filepath.Join("..", "fixtures", "config")
	ctx := context.Background()
	base, err := NewSession(afero.NewOsFs(), fixtures)
	assert.NilError(t, err)

	// baseline: the fixtures compile and every resource is locally clean
	_, err = base.Validate(ctx, CompileOptions{})
	assert.NilError(t, err, "fixtures must compile before we can attribute a failure to a deletion")

	root := GenerationRoot()
	checked := 0

	for _, k := range base.Kinds() {
		names, err := base.Names(k.Group, "")
		assert.NilError(t, err)
		if len(names) == 0 {
			continue
		}
		res, err := base.Address(k.Group, names[0], "")
		assert.NilError(t, err)

		for _, rf := range engine.RequiredFields(root, k.Group) {
			field := strings.Join(rf.Path, "/")
			name := k.Group + "." + field

			// Work on a fork so each deletion is independent.
			fork, err := base.Fork()
			assert.NilError(t, err)

			// Every location the field may live at — a branching path (AnyOf)
			// plus the legacy alias. Iterating only Path would silently skip a
			// field whose fixture authored the OTHER branch, and the test would
			// pass on coverage it never had.
			locs := rf.AnyOf
			if len(locs) == 0 {
				locs = [][]string{rf.Path}
			}
			if len(rf.Compat) > 0 {
				locs = append(append([][]string{}, locs...), rf.Compat)
			}
			removed := false
			for _, p := range locs {
				if len(p) == 0 {
					continue
				}
				if _, err := fork.Get(res, p); err == nil {
					assert.NilError(t, fork.Delete(res, p), name)
					removed = true
				}
			}
			if !removed {
				continue // absent already: the baseline proves it is not required here
			}
			checked++

			// Both sides must name one of the field's real locations — and the
			// SAME one, since each reports where it actually looked.
			names := map[string]bool{}
			for _, p := range locs {
				names[strings.Join(p, "/")] = true
			}
			flagged := false
			for _, i := range fork.ValidateResource(res) {
				if names[strings.Join(i.Field, "/")] {
					flagged = true
				}
			}

			// compiler: does it reject, naming one of them?
			_, cErr := fork.Validate(ctx, CompileOptions{})
			rejected := false
			if cErr != nil {
				for n := range names {
					if strings.Contains(cErr.Error(), n) {
						rejected = true
					}
				}
			}

			assert.Equal(t, flagged, rejected,
				"disagreement on %s after deleting it — partial flagged=%v, compiler rejected=%v (%v)",
				name, flagged, rejected, cErr)
		}
	}
	assert.Assert(t, checked >= 10, "expected to exercise a meaningful number of required fields, got %d", checked)
	t.Logf("required fields driven through both paths: %d", checked)
}

// The conditional half: a trigger-specific field must be required for the
// trigger that uses it and silent for every other one, at BOTH layers.
func TestConditionalRequiredFollowsTheDiscriminator(t *testing.T) {
	fixtures := filepath.Join("..", "fixtures", "config")
	ctx := context.Background()
	base, err := NewSession(afero.NewOsFs(), fixtures)
	assert.NilError(t, err)

	fn := []string{"functions", "probe_fn"}
	// per trigger type: the fields it must have beyond the universal ones
	perType := map[string][]string{
		"http":   {"trigger/domains", "trigger/paths", "trigger/method"},
		"https":  {"trigger/domains", "trigger/paths", "trigger/method"},
		"pubsub": {"trigger/channel"},
		"p2p":    {"trigger/protocol", "trigger/command"},
	}
	all := map[string]bool{}
	for _, fs := range perType {
		for _, f := range fs {
			all[f] = true
		}
	}

	for typ, want := range perType {
		fork, err := base.Fork()
		assert.NilError(t, err)
		// a resource carrying ONLY the universal requirements, plus the trigger
		assert.NilError(t, fork.Set(fn, []string{"id"}, "QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o"))
		assert.NilError(t, fork.Set(fn, []string{"source"}, "."))
		assert.NilError(t, fork.Set(fn, []string{"execution", "call"}, "ping"))
		assert.NilError(t, fork.Set(fn, []string{"execution", "timeout"}, "10s"))
		assert.NilError(t, fork.Set(fn, []string{"execution", "memory"}, "16MB"))
		assert.NilError(t, fork.Set(fn, []string{"trigger", "type"}, typ))

		got := map[string]bool{}
		for _, i := range fork.ValidateResource(fn) {
			got[strings.Join(i.Field, "/")] = true
		}
		for f := range all {
			assert.Equal(t, got[f], contains(want, f),
				"type=%s: %s required=%v but reported=%v", typ, f, contains(want, f), got[f])
		}

		// and the compiler agrees that it is incomplete...
		_, cErr := fork.Validate(ctx, CompileOptions{})
		assert.Assert(t, cErr != nil, "type=%s: compiler should reject a function missing %v", typ, want)

		// ...until the trigger's own fields are supplied, and then it does not.
		for _, f := range want {
			v := any("x")
			if strings.HasSuffix(f, "method") {
				v = "GET"
			}
			if strings.HasSuffix(f, "domains") || strings.HasSuffix(f, "paths") {
				v = []any{"test_domain1"}
				if strings.HasSuffix(f, "paths") {
					v = []any{"/probe"}
				}
			}
			assert.NilError(t, fork.Set(fn, strings.Split(f, "/"), v))
		}
		assert.Equal(t, len(fork.ValidateResource(fn)), 0, "type=%s should be complete now", typ)
		_, cErr = fork.Validate(ctx, CompileOptions{})
		assert.NilError(t, cErr, "type=%s should compile once its trigger fields are set", typ)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
