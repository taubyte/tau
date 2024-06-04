package application_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/application"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestOpenErrors(t *testing.T) {
	seer, err := internal.NewSeer()
	assert.NilError(t, err)

	_, err = application.Open(seer, "")
	assert.ErrorContains(t, err, "on application ``; name is empty")

	_, err = application.Open(nil, "test_app1")
	assert.ErrorContains(t, err, "on application `test_app1`; seer is nil")
}
