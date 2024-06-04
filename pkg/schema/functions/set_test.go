package functions_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/functions"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestSetBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	fun, err := project.Function("test_function1", "")
	assert.NilError(t, err)

	assertFunction1_http(t, fun.Get())

	var (
		id          = "function4ID"
		description = "this is test fun 4"
		tags        = []string{"fun_tag_7", "fun_tag_8"}
		domains     = []string{"dom_1", "dom_2"}
		method      = "put"
		source      = "library/test_lib1"
		call        = "test_lib1.ping"
	)

	err = fun.Set(true,
		functions.Id(id),
		functions.Description(description),
		functions.Tags(tags),
		functions.Domains(domains),
		functions.Method(method),
		functions.Source(source),
		functions.Call(call),
	)
	assert.NilError(t, err)

	assertion := func(_fun functions.Function) {
		eql(t, [][]any{
			{_fun.Get().Id(), id},
			{_fun.Get().Name(), "test_function1"},
			{_fun.Get().Description(), description},
			{_fun.Get().Tags(), tags},
			{_fun.Get().Domains(), domains},
			{_fun.Get().Method(), method},
			{_fun.Get().Source(), source},
			{_fun.Get().Call(), call},
			{_fun.Get().Application(), ""},
		})
	}
	assertion(fun)

	fun, err = project.Function("test_function1", "")
	assert.NilError(t, err)

	assertion(fun)
}

func TestSetInApp(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	fun, err := project.Function("test_function2", "test_app1")
	assert.NilError(t, err)

	assertFunction2_pubsub(t, fun.Get())

	var (
		id          = "function4ID"
		description = "this is test fun 4"
		tags        = []string{"fun_tag_7", "fun_tag_8"}
		channel     = "channel6"
		local       = false
		source      = "library/test_lib1"
		call        = "test_lib.ping_pubsub"
	)

	err = fun.Set(true,
		functions.Id(id),
		functions.Description(description),
		functions.Tags(tags),
		functions.Local(local),
		functions.Channel(channel),
		functions.Source(source),
		functions.Call(call),
	)
	assert.NilError(t, err)

	assertion := func(_fun functions.Function) {
		eql(t, [][]any{
			{_fun.Get().Id(), id},
			{_fun.Get().Name(), "test_function2"},
			{_fun.Get().Description(), description},
			{_fun.Get().Tags(), tags},
			{_fun.Get().Local(), local},
			{_fun.Get().Channel(), channel},
			{_fun.Get().Source(), source},
			{_fun.Get().Call(), call},
			{_fun.Get().Application(), "test_app1"},
		})
	}
	assertion(fun)

	fun, err = project.Function("test_function2", "test_app1")
	assert.NilError(t, err)

	assertion(fun)
}
