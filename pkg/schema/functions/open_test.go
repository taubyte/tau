package functions_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/functions"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestOpenErrors(t *testing.T) {
	seer, err := internal.NewSeer()
	assert.NilError(t, err)

	_, err = functions.Open(seer, "", "")
	assert.ErrorContains(t, err, "on function ``; name is empty")

	_, err = functions.Open(nil, "test_function1", "")
	assert.ErrorContains(t, err, "on function `test_function1`; seer is nil")
}
