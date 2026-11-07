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

// Every session binding is optional, so an unwired one reads as "no rule"; these
// tests fail when a part of the binding goes missing.

func session(t *testing.T) *schema.Session {
	t.Helper()
	s, err := schema.NewSession(afero.NewOsFs(), "../fixtures/basic")
	assert.NilError(t, err)
	return s
}

func TestValidateFieldRefusesWhatTheVocabularyRefuses(t *testing.T) {
	s := session(t)
	fn := []string{"function", "checkout"}

	type testCase struct {
		what  string
		res   []string
		field []string
		bad   any
		good  any
	}
	for _, c := range []testCase{
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

func TestValidateFieldReadsABlockAsABlock(t *testing.T) {
	s := session(t)
	fn := []string{"function", "checkout"}
	pkg := []string{"package", "paylib"}

	assert.NilError(t, s.ValidateField(fn, []string{"package"}, map[string]any{"name": "paylib"}))
	assert.NilError(t, s.ValidateField(pkg, []string{"source"}, map[string]any{"git": "https://github.com/acme/paylib"}))
	assert.NilError(t, s.ValidateField(pkg, []string{"source"}, "agents/support"))
	assert.NilError(t, s.ValidateField(fn, []string{"package", "name"}, "paylib"))

	// the leaves keep their own checks
	assert.Assert(t, s.ValidateField(fn, []string{"package", "name"}, "not a name!") != nil,
		"a block's leaf stopped being checked")
	// and a mapping under a key that heads no block is still the wrong type
	assert.Assert(t, s.ValidateField(fn, []string{"name"}, map[string]any{"x": "y"}) != nil,
		"any mapping at all now validates clean")
}

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

func TestValidateResourceAttributesWhatIsWrong(t *testing.T) {
	s := session(t)
	fn := []string{"function", "checkout"}
	assert.NilError(t, s.Set(fn, []string{"execution", "timeout"}, "20 parsecs"))

	issues := s.ValidateResource(fn)
	assert.Equal(t, len(issues), 1, "%v", issues)
	assert.DeepEqual(t, issues[0].Field, []string{"execution", "timeout"})

	// a required field that is absent has no value to check, so it is looked for by name
	assert.NilError(t, s.Delete(fn, []string{"execution", "memory"}))
	found := false
	for _, i := range s.ValidateResource(fn) {
		found = found || strings.Join(i.Field, "/") == "execution/memory"
	}
	assert.Assert(t, found, "a required field went missing and nothing said so")
}

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

// A required field of an entry is looked for in every entry the document has: a port
// with no number, a step with no toolchain.
func TestValidateResourceLooksInsideEveryEntry(t *testing.T) {
	s := session(t)

	app := []string{"service", "api"}
	assert.NilError(t, s.Delete(app, []string{"ports", "metrics", "number"}))
	pkg := []string{"package", "shop_site"}
	assert.NilError(t, s.Delete(pkg, []string{"build", "steps", "0", "image"}))

	found := map[string]bool{}
	for _, res := range [][]string{app, pkg} {
		for _, i := range s.ValidateResource(res) {
			found[strings.Join(i.Field, "/")] = true
		}
	}
	assert.Assert(t, found["ports/metrics/number"], "%v", found)
	assert.Assert(t, found["build/steps/0/image"], "%v", found)
	assert.Assert(t, !found["ports/http/number"], "an entry that has it owes nothing: %v", found)
}
