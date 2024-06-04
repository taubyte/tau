package messaging_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/messaging"
	"gotest.tools/v3/assert"
)

func TestDeleteBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	msg, err := project.Messaging("test_messaging2", "test_app1")
	assert.NilError(t, err)

	assertMessaging2(t, msg.Get())

	err = msg.Delete()
	assert.NilError(t, err)
	internal.AssertEmpty(t,
		msg.Get().Id(),
		msg.Get().Name(),
		msg.Get().Description(),
		msg.Get().Tags(),
		msg.Get().Local(),
		msg.Get().Regex(),
		msg.Get().ChannelMatch(),
		msg.Get().MQTT(),
		msg.Get().WebSocket(),
	)

	local, _ := project.Get().Messaging("test_app1")
	assert.Equal(t, len(local), 0)

	msg, err = project.Messaging("test_messaging2", "test_app1")
	assert.NilError(t, err)

	assert.Equal(t, msg.Get().Name(), "test_messaging2")
	internal.AssertEmpty(t,
		msg.Get().Id(),
		msg.Get().Description(),
		msg.Get().Tags(),
		msg.Get().Local(),
		msg.Get().Regex(),
		msg.Get().ChannelMatch(),
		msg.Get().MQTT(),
		msg.Get().WebSocket(),
	)
}

func TestDeleteAttributes(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	msg, err := project.Messaging("test_messaging2", "test_app1")
	assert.NilError(t, err)

	assertMessaging2(t, msg.Get())

	err = msg.Delete("description", "channel", "bridges")
	assert.NilError(t, err)

	assertion := func(_msg messaging.Messaging) {
		eql(t, [][]any{
			{_msg.Get().Id(), "messaging2ID"},
			{_msg.Get().Name(), "test_messaging2"},
			{_msg.Get().Description(), ""},
			{_msg.Get().Tags(), []string{"messaging_tag_3", "messaging_tag_4"}},
			{msg.Get().Local(), true},
			{msg.Get().Regex(), false},
			{msg.Get().ChannelMatch(), ""},
			{msg.Get().MQTT(), false},
			{msg.Get().WebSocket(), false},
			{msg.Get().Application(), "test_app1"},
		})
	}
	assertion(msg)

	// Re-open
	msg, err = project.Messaging("test_messaging2", "test_app1")
	assert.NilError(t, err)

	assert.Equal(t, msg.Get().Id(), "messaging2ID")
	assert.Equal(t, msg.Get().Name(), "test_messaging2")
	assertion(msg)
}
