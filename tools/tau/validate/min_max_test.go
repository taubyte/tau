package validate_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/validate"
	"gotest.tools/v3/assert"
)

func TestVariableMinValidator(t *testing.T) {
	assert.NilError(t, validate.VariableMinValidator(""))
	assert.NilError(t, validate.VariableMinValidator("0"))
	assert.NilError(t, validate.VariableMinValidator("42"))
	err := validate.VariableMinValidator("abc")
	assert.ErrorContains(t, err, "min value")
}

func TestVariableMaxValidator(t *testing.T) {
	assert.NilError(t, validate.VariableMaxValidator(""))
	assert.NilError(t, validate.VariableMaxValidator("100"))
	err := validate.VariableMaxValidator("notanum")
	assert.ErrorContains(t, err, "max value")
}
