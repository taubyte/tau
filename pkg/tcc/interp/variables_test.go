package interp

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

// THE BINDING IS AN INTERSECTION, WHICH IS WHY THIS PACKAGE SPELLS NO VARIABLE NAME.
// The root states which of the compile parameters are axes; everything else in the bag
// is a parameter some declaration reads, and a compiler that had to know the difference
// would be a compiler with one vocabulary's words in it.
func TestOnlyTheVariablesTheRootDeclaresAreBound(t *testing.T) {
	root := engine.Root(nil).With(engine.Varying("alpha", "beta"))
	c := &Compiler{
		compileRoot: root,
		env:         Env{"alpha": "a1", "beta": "b1", "cloud": "somewhere", "branch": "main"},
	}
	assert.DeepEqual(t, engine.Bound{"alpha": "a1", "beta": "b1"}, c.bound())
}

func TestAVocabularyThatDeclaresNoVariableBindsNothing(t *testing.T) {
	c := &Compiler{compileRoot: engine.Root(nil), env: Env{"alpha": "a1"}}
	assert.Assert(t, c.bound() == nil)
}

// An EMPTY value is a caller saying "the bare key", so it is left out rather than
// carried down as a binding nothing can honour.
func TestAnUnsetOrEmptyVariableIsNotBound(t *testing.T) {
	root := engine.Root(nil).With(engine.Varying("alpha", "beta"))
	c := &Compiler{compileRoot: root, env: Env{"alpha": ""}}
	assert.Assert(t, c.bound() == nil)

	c = &Compiler{compileRoot: root, env: Env{"alpha": "", "beta": "b1"}}
	assert.DeepEqual(t, engine.Bound{"beta": "b1"}, c.bound())
}

// One call per variable, and the values are the PROJECT'S: the variables themselves
// are the root's to declare, so this option cannot invent one.
func TestWithVariableCollectsOneSetPerVariable(t *testing.T) {
	c := &Compiler{}
	assert.NilError(t, WithVariable("alpha", "a1", "a2")(c))
	assert.NilError(t, WithVariable("beta", "b1")(c))
	assert.DeepEqual(t, map[string][]string{"alpha": {"a1", "a2"}, "beta": {"b1"}}, c.varValues)
}

// The lowered pass reads the same documents back, so the declared values travel with
// it — without them it is a compile that does not know a variant spelling is a field.
func TestTheDeclaredValuesTravelToTheLoweredPass(t *testing.T) {
	c := &Compiler{env: Env{}, varValues: map[string][]string{"alpha": {"a1", "a2"}}}
	carried := &Compiler{env: Env{}}
	for _, option := range c.viewOptions() {
		assert.NilError(t, option(carried))
	}
	assert.DeepEqual(t, c.varValues, carried.varValues)
}
