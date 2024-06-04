package messaging_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestGetStruct(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	msg, err := project.Messaging("test_messaging1", "")
	assert.NilError(t, err)

	_struct, err := msg.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "messaging1ID"},
		{_struct.Name, "test_messaging1"},
		{_struct.Description, "a messaging channel"},
		{_struct.Tags, []string{"messaging_tag_1", "messaging_tag_2"}},
		{_struct.Local, false},
		{_struct.Regex, false},
		{_struct.Match, "simple1"},
		{_struct.MQTT, false},
		{_struct.WebSocket, true},
		{len(_struct.SmartOps), 0},
	})

	msg, err = project.Messaging("test_messaging2", "test_app1")
	assert.NilError(t, err)

	_struct, err = msg.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "messaging2ID"},
		{_struct.Name, "test_messaging2"},
		{_struct.Description, "another messaging channel"},
		{_struct.Tags, []string{"messaging_tag_3", "messaging_tag_4"}},
		{_struct.Local, true},
		{_struct.Regex, true},
		{_struct.Match, "simple2"},
		{_struct.MQTT, true},
		{_struct.WebSocket, false},
		{len(_struct.SmartOps), 0},
	})
}
