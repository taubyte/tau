package messaging_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/messaging"
	"gotest.tools/v3/assert"
)

func TestOpenErrors(t *testing.T) {
	seer, err := internal.NewSeer()
	assert.NilError(t, err)

	_, err = messaging.Open(seer, "", "")
	assert.ErrorContains(t, err, "on messaging ``; name is empty")

	_, err = messaging.Open(nil, "test_messaging1", "")
	assert.ErrorContains(t, err, "on messaging `test_messaging1`; seer is nil")
}
