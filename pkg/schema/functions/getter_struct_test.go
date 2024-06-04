package functions_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/common"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestGetStruct(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	fun, err := project.Function("test_function1", "")
	assert.NilError(t, err)

	_struct, err := fun.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "function1ID"},
		{_struct.Name, "test_function1"},
		{_struct.Description, "an http function for a simple ping"},
		{_struct.Tags, []string{"function_tag_1", "function_tag_2"}},
		{_struct.Type, "http"},
		{_struct.Method, "get"},
		{_struct.Paths, []string{"/ping1"}},
		{_struct.Local, false},
		{_struct.Command, ""},
		{_struct.Channel, ""},
		{_struct.Source, "."},
		{_struct.Domains, []string{"test_domain1"}},
		{common.TimeToString(_struct.Timeout), "20s"},
		{common.UnitsToString(_struct.Memory), "32GB"},
		{_struct.Call, "ping1"},
		{_struct.Protocol, ""},
		{len(_struct.SmartOps), 0},
	})

	fun, err = project.Function("test_function2", "test_app1")
	assert.NilError(t, err)

	_struct, err = fun.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "function2ID"},
		{_struct.Name, "test_function2"},
		{_struct.Description, "a pubsub function on channel 2 with a call to a library"},
		{_struct.Tags, []string{"function_tag_3", "function_tag_4"}},
		{_struct.Type, "pubsub"},
		{_struct.Method, ""},
		{len(_struct.Paths), 0},
		{_struct.Local, true},
		{_struct.Command, ""},
		{_struct.Channel, "channel2"},
		{_struct.Source, "library/test_library2"},
		{len(_struct.Domains), 0},
		{common.TimeToString(_struct.Timeout), "23s"},
		{common.UnitsToString(_struct.Memory), "23MB"},
		{_struct.Call, "ping2"},
		{_struct.Protocol, ""},
		{len(_struct.SmartOps), 0},
	})

	fun, err = project.Function("test_function3", "test_app2")
	assert.NilError(t, err)

	_struct, err = fun.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "function3ID"},
		{_struct.Name, "test_function3"},
		{_struct.Description, "a p2p function for ping over peer-2-peer"},
		{_struct.Tags, []string{"function_tag_5", "function_tag_6"}},
		{_struct.Type, "p2p"},
		{_struct.Method, ""},
		{len(_struct.Paths), 0},
		{_struct.Local, false},
		{_struct.Command, "command3"},
		{_struct.Channel, ""},
		{_struct.Source, "."},
		{len(_struct.Domains), 0},
		{common.TimeToString(_struct.Timeout), "1h15m"},
		{common.UnitsToString(_struct.Memory), "64GB"},
		{_struct.Call, "ping3"},
		{_struct.Protocol, ""},
		{len(_struct.SmartOps), 0},
	})
}
