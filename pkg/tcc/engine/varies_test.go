package engine

import (
	"strings"
	"testing"

	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

// The vocabulary under test declares TWO variables, which no vocabulary in tree does
// yet — the whole point of the axis being generic. Its words are deliberately
// meaningless: this package must not be able to say what either variable is for.
func variableSchema(vars ...string) Schema {
	return SchemaDefinition(Root(
		Attributes(String("id")),
		DefineGroup("things",
			DefineIter([]*Attribute{
				String("id", Required()),
				String("solo", Varies("alpha")),
				String("both", Varies("alpha", "beta")),
				String("fixed"),
				String("switched", Path("pick", Either("one", "two")), Captures()),
				String("under", Path("pick", Either("one", "two"), "under"), Varies("alpha")),
			}),
		),
	).With(Strict(), Varying(vars...)))
}

func readWith(t *testing.T, schema Schema, values map[string][]string, b Bound, doc string) (map[string]any, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/config.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/things/one.yaml", []byte(doc), 0o644))

	e, err := NewWith(schema, values, yaseer.VirtualFS(fs, "/"))
	if err != nil {
		return nil, err
	}
	obj, err := e.ProcessFor(b)
	if err != nil {
		return nil, err
	}
	flat, _ := obj.Flat()["things"].(map[string]any)
	one, _ := flat["one"].(map[string]any)
	return one, nil
}

func twoVariables() map[string][]string {
	return map[string][]string{"alpha": {"a1", "a2"}, "beta": {"b1"}}
}

func TestAVariantIsReadInsteadOfTheKeyItSitsBeside(t *testing.T) {
	one, err := readWith(t, variableSchema("alpha", "beta"), twoVariables(), Bound{"alpha": "a1"}, `
id: one
solo: base
solo_a1: override
fixed: untouched
`)
	assert.NilError(t, err)
	assert.Equal(t, one["solo"], "override")
	assert.Equal(t, one["fixed"], "untouched")
}

func TestAVariantForAValueNobodyIsReadingLeavesTheKeyAlone(t *testing.T) {
	one, err := readWith(t, variableSchema("alpha", "beta"), twoVariables(), Bound{"alpha": "a2"}, `
id: one
solo: base
solo_a1: for the other value
`)
	assert.NilError(t, err)
	assert.Equal(t, one["solo"], "base")
}

// The capability this change exists for: ONE attribute, TWO axes, either of which may
// be the one written.
func TestAnAttributeMayVaryByMoreThanOneVariable(t *testing.T) {
	for _, tc := range []struct {
		bound Bound
		doc   string
		want  string
		why   string
	}{
		{Bound{"alpha": "a1"}, "both: base\nboth_a1: by alpha\n", "by alpha", "the first variable's variant is written"},
		{Bound{"beta": "b1"}, "both: base\nboth_b1: by beta\n", "by beta", "the second variable's is, and nothing about the first applies"},
		{Bound{"alpha": "a1", "beta": "b1"}, "both: base\nboth_b1: by beta\n", "by beta", "both are bound and only one is written, so there is no contest"},
		{Bound{"alpha": "a2", "beta": "b1"}, "both: base\n", "base", "neither is written"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			one, err := readWith(t, variableSchema("alpha", "beta"), twoVariables(), tc.bound, "id: one\n"+tc.doc)
			assert.NilError(t, err)
			assert.Equal(t, one["both"], tc.want, tc.why)
		})
	}
}

// PRECEDENCE IS NOT THE ANSWER TO TWO VARIANTS AT ONCE. The author stated two values
// for one field, and quietly taking the root's first variable would be a decision
// nobody wrote.
func TestTwoVariantsWrittenAtOnceIsAConflictThatNamesBoth(t *testing.T) {
	_, err := readWith(t, variableSchema("alpha", "beta"), twoVariables(), Bound{"alpha": "a1", "beta": "b1"}, `
id: one
both: base
both_a1: by alpha
both_b1: by beta
`)
	assert.ErrorContains(t, err, "written for both")
	assert.ErrorContains(t, err, "both_a1")
	assert.ErrorContains(t, err, "both_b1")
}

// A matcher spells its own suffix, so a variant lands on whichever alternative the
// document chose — and the bare alternative still names the choice.
func TestAVariantLandsOnTheAlternativeTheDocumentChose(t *testing.T) {
	one, err := readWith(t, variableSchema("alpha", "beta"), twoVariables(), Bound{"alpha": "a1"}, `
id: one
pick:
    two:
        under: base
        under_a1: override
`)
	assert.NilError(t, err)
	assert.Equal(t, one["under"], "override")
	assert.Equal(t, one["switched"], "two", "the unsuffixed alternative is what was taken")
}

// A VARIANT OF AN UNMARKED KEY IS STILL A TYPO. Strict claims the suffixed spelling
// only where the attribute says it varies.
func TestASuffixOnAKeyThatDoesNotVaryIsUnknown(t *testing.T) {
	_, err := readWith(t, variableSchema("alpha", "beta"), twoVariables(), Bound{"alpha": "a1"}, `
id: one
fixed: base
fixed_a1: not a thing
`)
	assert.ErrorContains(t, err, "fixed_a1")
}

// Strict claims the UNION of every variant of every variable, not the cross product:
// there are no compound suffixes, so `both_a1_b1` is nobody's key.
func TestStrictClaimsEachVariantAndNoCompoundOfThem(t *testing.T) {
	_, err := readWith(t, variableSchema("alpha", "beta"), twoVariables(), Bound{"alpha": "a1"}, `
id: one
both: base
both_a1_b1: not a thing
`)
	assert.ErrorContains(t, err, "both_a1_b1")

	_, err = readWith(t, variableSchema("alpha", "beta"), twoVariables(), nil, `
id: one
both: base
both_a1: for one reading
both_a2: for another
both_b1: for a third
`)
	assert.NilError(t, err, "every declared value's spelling is a key this schema can read, whichever one is being read")
}

func TestAnAttributeCannotVaryByAVariableTheRootDoesNotDeclare(t *testing.T) {
	_, err := readWith(t, variableSchema("alpha"), map[string][]string{"alpha": {"a1"}}, nil, "id: one\n")
	assert.ErrorContains(t, err, `"both" varies by "beta"`)
	assert.ErrorContains(t, err, "which the root does not declare")
}

func TestValuesForAVariableTheRootDoesNotDeclareAreRefused(t *testing.T) {
	_, err := readWith(t, variableSchema("alpha"), map[string][]string{"gamma": {"g1"}}, nil, "id: one\n")
	assert.ErrorContains(t, err, `values given for "gamma"`)
}

// Two axes are free to admit the same word — until ONE attribute varies by both, where
// the key would mean two things.
func TestTwoVariablesOfOneAttributeMayNotShareAValue(t *testing.T) {
	_, err := readWith(t, variableSchema("alpha", "beta"), map[string][]string{"alpha": {"shared"}, "beta": {"shared"}}, nil, "id: one\n")
	assert.ErrorContains(t, err, "both admit the value \"shared\"")
	assert.ErrorContains(t, err, "both_shared would mean two things")
}

// THE ERROR POINTS AT THE OPTIONS, NOT AT SOMEBODY'S DOCUMENT. Without this the read
// succeeds, claims no suffix, and strict rejects the author's own override as an
// unknown key — an error about the wrong file entirely.
func TestBindingAVariableWithNoValuesIsRefusedLoudly(t *testing.T) {
	_, err := readWith(t, variableSchema("alpha", "beta"), map[string][]string{"alpha": {"a1"}}, Bound{"beta": "b1"}, "id: one\n")
	assert.ErrorContains(t, err, `bound "beta" to "b1"`)
	assert.ErrorContains(t, err, "declares no values for it")
}

// An EMPTY value is how a caller says "the bare key", which is what a project with no
// values reads anyway — so it is not a binding and not an error.
func TestAnEmptyValueIsNotABinding(t *testing.T) {
	one, err := readWith(t, variableSchema("alpha", "beta"), nil, Bound{"alpha": ""}, `
id: one
solo: base
`)
	assert.NilError(t, err)
	assert.Equal(t, one["solo"], "base")
}

// A vocabulary that declares no variable is untouched by all of it: no suffix is
// claimed, so every one is a typo, and an unbound read is the only read there is.
func TestAVocabularyWithNoVariablesReadsTheBareKeys(t *testing.T) {
	plain := SchemaDefinition(Root(
		Attributes(String("id")),
		DefineGroup("things", DefineIter([]*Attribute{String("id", Required()), String("solo")})),
	).With(Strict()))

	one, err := readWith(t, plain, nil, nil, "id: one\nsolo: base\n")
	assert.NilError(t, err)
	assert.Equal(t, one["solo"], "base")

	_, err = readWith(t, plain, nil, nil, "id: one\nsolo: base\nsolo_a1: nope\n")
	assert.ErrorContains(t, err, "solo_a1")
}

// Varying states PRECEDENCE, and precedence is the root's to state: the order a caller
// passed its values in must not reach the resolution. Two roots differing only in the
// order they list their variables are two different readings of one document — which is
// only observable where the reader looks, since two variants at once is a conflict.
func TestTheRootsOrderIsWhatIsTriedFirst(t *testing.T) {
	onDisk := yaseer.VirtualFS(afero.NewMemMapFs(), "/")

	e, err := NewWith(variableSchema("beta", "alpha"), twoVariables(), onDisk)
	assert.NilError(t, err)
	assert.DeepEqual(t, []string{"beta", "alpha"}, namesOf(e.(*instance).vars))

	e, err = NewWith(variableSchema("alpha", "beta"), twoVariables(), onDisk)
	assert.NilError(t, err)
	assert.DeepEqual(t, []string{"alpha", "beta"}, namesOf(e.(*instance).vars))
}

func namesOf(vars Vars) []string {
	out := make([]string, 0, len(vars))
	for _, v := range vars {
		out = append(out, v.Name)
	}
	return out
}

// Varies APPENDS, because Annotate overwrites and two marks on one attribute mean two
// axes rather than the second one only.
func TestTwoVariesOnOneAttributeMeanBothOfThem(t *testing.T) {
	a := String("x", Varies("alpha"), Varies("beta"))
	assert.DeepEqual(t, []string{"alpha", "beta"}, VariesBy(a))
	assert.Assert(t, VariesBy(String("y")) == nil)
}

func TestVariablesOfReadsBackWhatTheRootDeclared(t *testing.T) {
	assert.DeepEqual(t, []string{"alpha", "beta"}, VariablesOf(Root(nil).With(Varying("alpha", "beta"))))
	assert.Assert(t, VariablesOf(Root(nil)) == nil)
	assert.Assert(t, VariablesOf(nil) == nil)
}

func TestAMatcherThatCannotEnumerateItselfHasNoVariantSpelling(t *testing.T) {
	assert.Assert(t, variantPath([]StringMatch{"a", All()}, "a1") == nil)
	assert.Assert(t, variantPath([]StringMatch{"a", "b"}, "") == nil)
	assert.Assert(t, variantPath(nil, "a1") == nil)

	got := variantPath([]StringMatch{"a", "b"}, "a1")
	assert.Equal(t, got[len(got)-1], StringMatch("b_a1"))

	suffixed, ok := variantPath([]StringMatch{Either("one", "two")}, "a1")[0].(StringMatcher)
	assert.Assert(t, ok)
	assert.Assert(t, suffixed.Match("two_a1"))
	assert.Assert(t, !suffixed.Match("two"), "the bare alternative still names the choice")
	assert.Assert(t, strings.Contains(suffixed.String(), "one_a1"))
}

func TestInlinedInto(t *testing.T) {
	assert.Equal(t, InlinedInto(nil), "")
	a := &Attribute{Name: "file"}
	assert.Equal(t, InlinedInto(a), "")
	Inlined("records")(a)
	assert.Equal(t, InlinedInto(a), "records")
}
