package functions_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/taubyte/tau/pkg/schema/functions"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func eql(t *testing.T, a [][]any) {
	_, file, line, _ := runtime.Caller(2)
	for idx, pair := range a {
		switch pair[0].(type) {
		case []string:
			comp := cmp.DeepEqual(pair[0], pair[1])
			assert.Check(t, comp, fmt.Sprintf("item(%d): %s:%d", idx, file, line))
		default:
			assert.Equal(t, pair[0], pair[1], fmt.Sprintf("item(%d): %s:%d", idx, file, line))
		}
	}
}

func assertFunction1_http(t *testing.T, getter functions.Getter) {
	eql(t, [][]any{
		{getter.Id(), "function1ID"},
		{getter.Name(), "test_function1"},
		{getter.Description(), "an http function for a simple ping"},
		{getter.Tags(), []string{"function_tag_1", "function_tag_2"}},
		{getter.Type(), "http"},
		{getter.Method(), "get"},
		{getter.Paths(), []string{"/ping1"}},
		{getter.Local(), false},
		{getter.Command(), ""},
		{getter.Channel(), ""},
		{getter.Source(), "."},
		{getter.Domains(), []string{"test_domain1"}},
		{getter.Timeout(), "20s"},
		{getter.Memory(), "32GB"},
		{getter.Call(), "ping1"},
		{getter.Protocol(), ""},
		{getter.Application(), ""},
		{len(getter.SmartOps()), 0},
	})
}

func assertFunction2_pubsub(t *testing.T, getter functions.Getter) {
	eql(t, [][]any{
		{getter.Id(), "function2ID"},
		{getter.Name(), "test_function2"},
		{getter.Description(), "a pubsub function on channel 2 with a call to a library"},
		{getter.Tags(), []string{"function_tag_3", "function_tag_4"}},
		{getter.Type(), "pubsub"},
		{getter.Method(), ""},
		{len(getter.Paths()), 0},
		{getter.Local(), true},
		{getter.Command(), ""},
		{getter.Channel(), "channel2"},
		{getter.Source(), "library/test_library2"},
		{len(getter.Domains()), 0},
		{getter.Timeout(), "23s"},
		{getter.Memory(), "23MB"},
		{getter.Call(), "ping2"},
		{getter.Protocol(), ""},
		{getter.Application(), "test_app1"},
		{len(getter.SmartOps()), 0},
	})
}

func assertFunction3_p2p(t *testing.T, getter functions.Getter) {
	eql(t, [][]any{
		{getter.Id(), "function3ID"},
		{getter.Name(), "test_function3"},
		{getter.Description(), "a p2p function for ping over peer-2-peer"},
		{getter.Tags(), []string{"function_tag_5", "function_tag_6"}},
		{getter.Type(), "p2p"},
		{getter.Method(), ""},
		{len(getter.Paths()), 0},
		{getter.Local(), false},
		{getter.Command(), "command3"},
		{getter.Channel(), ""},
		{getter.Source(), "."},
		{len(getter.Domains()), 0},
		{getter.Timeout(), "1h15m"},
		{getter.Memory(), "64GB"},
		{getter.Call(), "ping3"},
		{getter.Protocol(), ""},
		{getter.Application(), "test_app2"},
		{len(getter.SmartOps()), 0},
	})
}

func TestGet(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	fun, err := project.Function("test_function1", "")
	assert.NilError(t, err)

	assertFunction1_http(t, fun.Get())

	fun, err = project.Function("test_function2", "test_app1")
	assert.NilError(t, err)

	assertFunction2_pubsub(t, fun.Get())

	fun, err = project.Function("test_function3", "test_app2")
	assert.NilError(t, err)

	assertFunction3_p2p(t, fun.Get())
}
