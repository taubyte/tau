package smartops_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/smartops"
	"gotest.tools/v3/assert"
)

func TestOpenErrors(t *testing.T) {
	seer, err := internal.NewSeer()
	assert.NilError(t, err)

	_, err = smartops.Open(seer, "", "")
	assert.ErrorContains(t, err, "on smartops ``; name is empty")

	_, err = smartops.Open(nil, "test_smartops1", "")
	assert.ErrorContains(t, err, "on smartops `test_smartops1`; seer is nil")
}
