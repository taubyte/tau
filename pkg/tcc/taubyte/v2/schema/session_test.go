package schema_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// A SESSION IS BOUND TO A VOCABULARY, and every part of that binding is optional to the
// generic session — so a part nobody wired is not an error anywhere. It reads as "this
// vocabulary has no rule": ValidateField returned nil for anything at all, and Generate
// answered "no generator wired" for the one field nobody authors. These are the tests
// that say the binding is there, and they are what fails when a part of it goes missing.

func session(t *testing.T) *schema.Session {
	t.Helper()
	s, err := schema.NewSession(afero.NewOsFs(), "../fixtures/basic")
	assert.NilError(t, err)
	return s
}

// The vocabulary's own per-value checks, run with no compile. Each of these is a rule
// the compiler enforces at load; the point of the binding is that they answer the same
// way between keystrokes, for a fraction of the cost.
func TestValidateFieldRefusesWhatTheVocabularyRefuses(t *testing.T) {
	s := session(t)
	fn := []string{"function", "checkout"}

	for _, c := range []struct {
		what  string
		res   []string
		field []string
		bad   any
		good  any
	}{
		// An id is a CID, and "an id" that is not one is how a resource ends up
		// addressed by something no other consumer can resolve.
		{"an id that is not a CID", fn, []string{"id"}, "not-a-cid", "QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh"},
		// A name is a variable name because everything downstream — an environment
		// variable, a generated host, a Go field — spells it back.
		{"a name that is not a variable name", fn, []string{"name"}, "not a name!", "checkout"},
		// A duration is parsed, not stored as prose.
		{"a duration nothing can parse", fn, []string{"execution", "timeout"}, "20 parsecs", "20s"},
		// A byte size likewise.
		{"a size nothing can parse", fn, []string{"execution", "memory"}, "some", "32MB"},
		// A closed set is closed. The list arrives as yaml decoded it — []any — which
		// is exactly how the enum went unchecked before.
		{"a method outside the set", []string{"gateway", "shop"}, []string{"route", "methods"}, []any{"YEET"}, []any{"POST"}},
		// A wrong type is a wrong value, before any validator runs.
		{"a bool where a string belongs", fn, []string{"execution", "call"}, true, "checkout"},
	} {
		t.Run(c.what, func(t *testing.T) {
			assert.Assert(t, s.ValidateField(c.res, c.field, c.bad) != nil,
				"%s was accepted: %v", c.what, c.bad)
			assert.NilError(t, s.ValidateField(c.res, c.field, c.good))
		})
	}

	// A path that names no field is not "valid, unconstrained" — a typo answering OK is
	// how a setting silently does nothing.
	assert.Assert(t, s.ValidateField(fn, []string{"executon", "timeout"}, "20s") != nil,
		"a misspelled field validated clean")
}

// `source` is one key that is either a value or a block (source.go), and the block's
// leaves are checked at their own paths. Reading the block as a bad string is what a
// validator wired straight through does, and it called every component-sourced resource
// in the fixtures broken.
func TestValidateFieldReadsABlockAsABlock(t *testing.T) {
	s := session(t)
	fn := []string{"function", "checkout"}

	assert.NilError(t, s.ValidateField(fn, []string{"source"}, map[string]any{"component": "paylib"}))
	assert.NilError(t, s.ValidateField(fn, []string{"source"}, "agents/support"))
	assert.NilError(t, s.ValidateField(fn, []string{"source", "component"}, "paylib"))

	// the leaves keep their own checks
	assert.Assert(t, s.ValidateField(fn, []string{"source", "component"}, "not a name!") != nil,
		"a block's leaf stopped being checked")
	// and a mapping under a key that heads no block is still the wrong type
	assert.Assert(t, s.ValidateField(fn, []string{"name"}, map[string]any{"x": "y"}) != nil,
		"any mapping at all now validates clean")
}

// THE FIXTURE COMPILES, so partial validation must find nothing in it. This is the half
// that is easy to lose: a check that fires on valid config is worse than no check, and
// every variant of "wire the validators" that got this wrong was rejected here first —
// a list read as the wrong type, a block read as a string, and a reference resolved by
// v1's scope rule instead of this vocabulary's (`authorizer: {name: tier_check,
// namespace: /shop}` in cache/orders.yaml is the one that catches that last one).
func TestValidateResourceFindsNothingInAConfigThatCompiles(t *testing.T) {
	const base = "../fixtures/basic"
	s := session(t)

	assert.NilError(t, filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".yaml") {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		res := strings.Split(strings.TrimSuffix(rel, ".yaml"), string(filepath.Separator))
		if len(res) == 1 {
			return nil // the project's own document, not a resource
		}
		for _, issue := range s.ValidateResource(res) {
			t.Errorf("%s: %s: %s", strings.Join(res, "/"), strings.Join(issue.Field, "/"), issue.Message)
		}
		return nil
	}))
}

// ...and it does find something once something is wrong, attributed to the field that is
// wrong, which is what an editor underlines.
func TestValidateResourceAttributesWhatIsWrong(t *testing.T) {
	s := session(t)
	fn := []string{"function", "checkout"}
	assert.NilError(t, s.Set(fn, []string{"execution", "timeout"}, "20 parsecs"))

	issues := s.ValidateResource(fn)
	assert.Equal(t, len(issues), 1, "%v", issues)
	assert.DeepEqual(t, issues[0].Field, []string{"execution", "timeout"})

	// a required field that is absent has no value to check, so it is looked for by name
	assert.NilError(t, s.Delete(fn, []string{"execution", "call"}))
	found := false
	for _, i := range s.ValidateResource(fn) {
		found = found || strings.Join(i.Field, "/") == "execution/call"
	}
	assert.Assert(t, found, "a required field went missing and nothing said so")
}

// An id is minted, never asked for, and the vocabulary says which fields those are — so
// no consumer carries its own "how do I make an id", which is how the two that did ended
// up disagreeing about it.
func TestGenerateMintsWhatNobodyAuthors(t *testing.T) {
	s := session(t)
	fn := []string{"function", "checkout"}

	id, err := s.Generate(fn, []string{"id"})
	assert.NilError(t, err)
	assert.NilError(t, s.ValidateField(fn, []string{"id"}, id),
		"the minted id is not a value the vocabulary accepts: %q", id)

	// Fresh entropy every call: two resources that share a seed still never collide,
	// and re-generating one never reproduces the old value.
	again, err := s.Generate(fn, []string{"id"})
	assert.NilError(t, err)
	assert.Assert(t, again != id, "two calls minted the same id: %q", id)

	// A field the author writes is not one to mint.
	_, err = s.Generate(fn, []string{"name"})
	assert.ErrorContains(t, err, "not a generated field")
}
