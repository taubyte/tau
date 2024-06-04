package messaging_test

import (
	_ "embed"
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/messaging"
	"gotest.tools/v3/assert"
)

func TestYaml(t *testing.T) {
	getter, err := messaging.Yaml("test_messaging1", "", internal.Messaging1)
	assert.NilError(t, err)

	assertMessaging1(t, getter)

	getter, err = messaging.Yaml("test_messaging2", "test_app1", internal.Messaging2)
	assert.NilError(t, err)

	assertMessaging2(t, getter)
}
