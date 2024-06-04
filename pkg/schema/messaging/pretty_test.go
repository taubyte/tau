package messaging_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestPretty(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	msg, err := project.Messaging("test_messaging1", "")
	assert.NilError(t, err)

	assert.DeepEqual(t, msg.Prettify(nil), map[string]interface{}{
		"Id":           "messaging1ID",
		"Name":         "test_messaging1",
		"Description":  "a messaging channel",
		"Tags":         []string{"messaging_tag_1", "messaging_tag_2"},
		"Local":        false,
		"Regex":        false,
		"ChannelMatch": "simple1",
		"MQTT":         false,
		"WebSocket":    true,
	})
}
