package messaging_test

import (
	"fmt"
	"runtime"
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/messaging"
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

func assertMessaging1(t *testing.T, getter messaging.Getter) {
	eql(t, [][]any{
		{getter.Id(), "messaging1ID"},
		{getter.Name(), "test_messaging1"},
		{getter.Description(), "a messaging channel"},
		{getter.Tags(), []string{"messaging_tag_1", "messaging_tag_2"}},
		{getter.Local(), false},
		{getter.Regex(), false},
		{getter.ChannelMatch(), "simple1"},
		{getter.MQTT(), false},
		{getter.WebSocket(), true},
		{getter.Application(), ""},
		{len(getter.SmartOps()), 0},
	})
}

func assertMessaging2(t *testing.T, getter messaging.Getter) {
	eql(t, [][]any{
		{getter.Id(), "messaging2ID"},
		{getter.Name(), "test_messaging2"},
		{getter.Description(), "another messaging channel"},
		{getter.Tags(), []string{"messaging_tag_3", "messaging_tag_4"}},
		{getter.Local(), true},
		{getter.Regex(), true},
		{getter.ChannelMatch(), "simple2"},
		{getter.MQTT(), true},
		{getter.WebSocket(), false},
		{getter.Application(), "test_app1"},
		{len(getter.SmartOps()), 0},
	})
}

func TestGet(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	msg, err := project.Messaging("test_messaging1", "")
	assert.NilError(t, err)

	assertMessaging1(t, msg.Get())

	msg, err = project.Messaging("test_messaging2", "test_app1")
	assert.NilError(t, err)

	assertMessaging2(t, msg.Get())
}
