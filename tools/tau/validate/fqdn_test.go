package validate_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/validate"
	"gotest.tools/v3/assert"
)

func TestVariableFQDN(t *testing.T) {
	err := validate.VariableFQDN("example.com")
	assert.NilError(t, err)

	err = validate.VariableFQDN("sub.example.com")
	assert.NilError(t, err)

	err = validate.VariableFQDN("invalid..fqdn")
	assert.ErrorContains(t, err, "not a valid fqdn")
}
