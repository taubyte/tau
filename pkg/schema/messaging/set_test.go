package messaging_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/messaging"
	"gotest.tools/v3/assert"
)

func TestSetBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	msg, err := project.Messaging("test_messaging1", "")
	assert.NilError(t, err)

	assertMessaging1(t, msg.Get())

	var (
		id          = "messaging3ID"
		description = "this is test msg 3"
		tags        = []string{"msg_tag_5", "msg_tag_6"}
		local       = true
		regex       = true
		match       = "^[0-9]*channel"
		mqtt        = true
		webSocket   = false
	)

	err = msg.Set(true,
		messaging.Id(id),
		messaging.Description(description),
		messaging.Tags(tags),
		messaging.Local(local),
		messaging.Channel(regex, match),
		messaging.Bridges(mqtt, webSocket),
	)
	assert.NilError(t, err)

	assertion := func(_msg messaging.Messaging) {
		eql(t, [][]any{
			{_msg.Get().Id(), id},
			{_msg.Get().Name(), "test_messaging1"},
			{_msg.Get().Description(), description},
			{_msg.Get().Tags(), tags},
			{_msg.Get().Local(), local},
			{_msg.Get().Regex(), regex},
			{_msg.Get().ChannelMatch(), match},
			{_msg.Get().MQTT(), mqtt},
			{_msg.Get().WebSocket(), webSocket},
			{_msg.Get().Application(), ""},
		})
	}
	assertion(msg)

	msg, err = project.Messaging("test_messaging1", "")
	assert.NilError(t, err)

	assertion(msg)
}

func TestSetInApp(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	msg, err := project.Messaging("test_messaging2", "test_app1")
	assert.NilError(t, err)

	assertMessaging2(t, msg.Get())

	var (
		id          = "messaging3ID"
		description = "this is test msg 3"
		tags        = []string{"msg_tag_5", "msg_tag_6"}
		local       = false
		regex       = false
		match       = "testChannel"
		mqtt        = false
		webSocket   = true
		smartOps    = []string{"smart1"}
	)

	err = msg.Set(true,
		messaging.Id(id),
		messaging.Description(description),
		messaging.Tags(tags),
		messaging.Local(local),
		messaging.Channel(regex, match),
		messaging.Bridges(mqtt, webSocket),
		messaging.SmartOps(smartOps),
	)
	assert.NilError(t, err)

	assertion := func(_msg messaging.Messaging) {
		eql(t, [][]any{
			{_msg.Get().Id(), id},
			{_msg.Get().Name(), "test_messaging2"},
			{_msg.Get().Description(), description},
			{_msg.Get().Tags(), tags},
			{_msg.Get().Local(), local},
			{_msg.Get().Regex(), regex},
			{_msg.Get().ChannelMatch(), match},
			{_msg.Get().MQTT(), mqtt},
			{_msg.Get().WebSocket(), webSocket},
			{_msg.Get().Application(), "test_app1"},
			{_msg.Get().SmartOps(), smartOps},
		})
	}
	assertion(msg)

	msg, err = project.Messaging("test_messaging2", "test_app1")
	assert.NilError(t, err)

	assertion(msg)
}
