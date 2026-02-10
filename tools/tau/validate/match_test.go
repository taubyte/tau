package validate_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/validate"
	"gotest.tools/v3/assert"
)

func TestVariableMatchValidator(t *testing.T) {
	assert.NilError(t, validate.VariableMatchValidator(""))
	assert.NilError(t, validate.VariableMatchValidator("any"))
}
