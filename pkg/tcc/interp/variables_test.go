package interp

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

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

func TestAnUnsetOrEmptyVariableIsNotBound(t *testing.T) {
	root := engine.Root(nil).With(engine.Varying("alpha", "beta"))
	c := &Compiler{compileRoot: root, env: Env{"alpha": ""}}
	assert.Assert(t, c.bound() == nil)

	c = &Compiler{compileRoot: root, env: Env{"alpha": "", "beta": "b1"}}
	assert.DeepEqual(t, engine.Bound{"beta": "b1"}, c.bound())
}

func TestWithVariableCollectsOneSetPerVariable(t *testing.T) {
	c := &Compiler{}
	assert.NilError(t, WithVariable("alpha", "a1", "a2")(c))
	assert.NilError(t, WithVariable("beta", "b1")(c))
	assert.DeepEqual(t, map[string][]string{"alpha": {"a1", "a2"}, "beta": {"b1"}}, c.varValues)
}

func TestTheDeclaredValuesTravelToTheLoweredPass(t *testing.T) {
	c := &Compiler{env: Env{}, varValues: map[string][]string{"alpha": {"a1", "a2"}}}
	carried := &Compiler{env: Env{}}
	for _, option := range c.viewOptions() {
		assert.NilError(t, option(carried))
	}
	assert.DeepEqual(t, c.varValues, carried.varValues)
}
