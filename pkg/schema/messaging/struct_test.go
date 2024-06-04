package messaging_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"gotest.tools/v3/assert"
)

func TestStructBasic(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	msg, err := project.Messaging("test_messaging1", "")
	assert.NilError(t, err)

	err = msg.SetWithStruct(true, &structureSpec.Messaging{
		Id:          "messaging1ID",
		Description: "a messaging channel",
		Tags:        []string{"messaging_tag_1", "messaging_tag_2"},
		Match:       "simple1",
		WebSocket:   true,
	})
	assert.NilError(t, err)

	assertMessaging1(t, msg.Get())

	msg, err = project.Messaging("test_messaging2", "test_app1")
	assert.NilError(t, err)

	// Use different cert type
	err = msg.SetWithStruct(true, &structureSpec.Messaging{
		Id:          "messaging2ID",
		Description: "another messaging channel",
		Tags:        []string{"messaging_tag_3", "messaging_tag_4"},
		Local:       true,
		Regex:       true,
		Match:       "simple2",
		MQTT:        true,
	})
	assert.NilError(t, err)

	assertMessaging2(t, msg.Get())

	err = msg.SetWithStruct(true, &structureSpec.Messaging{
		SmartOps: []string{"smart1"},
	})
	assert.NilError(t, err)
	assert.DeepEqual(t, msg.Get().SmartOps(), []string{"smart1"})
}

func TestStructError(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	msg, err := project.Messaging("test_messaging1", "")
	assert.NilError(t, err)

	err = msg.SetWithStruct(true, nil)
	assert.ErrorContains(t, err, "nil pointer")
}
