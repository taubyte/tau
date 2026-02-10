package validate_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/validate"
	"gotest.tools/v3/assert"
)

func TestVariableTypeValidator(t *testing.T) {
	assert.NilError(t, validate.VariableTypeValidator(""))
	assert.NilError(t, validate.VariableTypeValidator("short"))
	long := string(make([]byte, 251))
	err := validate.VariableTypeValidator(long)
	assert.ErrorContains(t, err, "250")
}
