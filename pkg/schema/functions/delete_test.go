package functions_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/functions"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func empty(t *testing.T, fun functions.Function) {
	internal.AssertEmpty(t,
		fun.Get().Id(),
		fun.Get().Description(),
		fun.Get().Tags(),
		fun.Get().Type(),
		fun.Get().Method(),
		fun.Get().Paths(),
		fun.Get().Local(),
		fun.Get().Command(),
		fun.Get().Channel(),
		fun.Get().Source(),
		fun.Get().Domains(),
		fun.Get().Timeout(),
		fun.Get().Memory(),
		fun.Get().Call(),
		fun.Get().Protocol(),
		fun.Get().SmartOps(),
	)
}

func TestDeleteBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	_, global := project.Get().Functions("")
	assert.Equal(t, len(global), 1)

	fun, err := project.Function("test_function1", "")
	assert.NilError(t, err)

	assertFunction1_http(t, fun.Get())

	err = fun.Delete()
	assert.NilError(t, err)
	empty(t, fun)

	fun, err = project.Function("test_function1", "")
	assert.NilError(t, err)
	empty(t, fun)

	_, global = project.Get().Functions("")
	assert.Equal(t, len(global), 0)

	// Re-open
	fun, err = project.Function("test_function1", "")
	assert.NilError(t, err)
	empty(t, fun)

	// In app
	local, _ := project.Get().Functions("test_app1")
	assert.Equal(t, len(local), 1)

	fun, err = project.Function("test_function2", "test_app1")
	assert.NilError(t, err)

	assertFunction2_pubsub(t, fun.Get())

	err = fun.Delete()
	assert.NilError(t, err)
	empty(t, fun)

	local, _ = project.Get().Functions("test_app1")
	assert.Equal(t, len(local), 0)

	// Re-open
	fun, err = project.Function("test_function2", "test_app1")
	assert.NilError(t, err)
	empty(t, fun)
}

func TestDeleteAttributes(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	fun, err := project.Function("test_function1", "")
	assert.NilError(t, err)

	assertFunction1_http(t, fun.Get())

	err = fun.Delete("description", "trigger")
	assert.NilError(t, err)

	assertion := func(_fun functions.Function) {
		internal.AssertEmpty(t,
			_fun.Get().Description(),
			_fun.Get().Type(),
			_fun.Get().Method(),
			_fun.Get().Paths(),
		)

		eql(t, [][]any{
			{fun.Get().Id(), "function1ID"},
			{fun.Get().Name(), "test_function1"},
			{fun.Get().Description(), ""},
			{fun.Get().Tags(), []string{"function_tag_1", "function_tag_2"}},
			{fun.Get().Type(), ""},
			{fun.Get().Method(), ""},
			{len(fun.Get().Paths()), 0},
			{fun.Get().Local(), false},
			{fun.Get().Command(), ""},
			{fun.Get().Channel(), ""},
			{fun.Get().Source(), "."},
			{fun.Get().Domains(), []string{"test_domain1"}},
			{fun.Get().Timeout(), "20s"},
			{fun.Get().Memory(), "32GB"},
			{fun.Get().Call(), "ping1"},
			{fun.Get().Protocol(), ""},
			{fun.Get().Application(), ""},
			{len(fun.Get().SmartOps()), 0},
		})

	}
	assertion(fun)

	// Re-open
	fun, err = project.Function("test_function1", "")
	assert.NilError(t, err)

	assertion(fun)
}
