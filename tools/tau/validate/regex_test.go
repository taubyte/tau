package validate_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/validate"
	"gotest.tools/v3/assert"
)

func TestNameRegex_MatchAllString(t *testing.T) {
	// Valid name: starts with letter, then letters/numbers/underscores/dashes
	assert.NilError(t, validate.MatchAllString("a", validate.NameRegex))
	assert.NilError(t, validate.MatchAllString("abc_123", validate.NameRegex))
	assert.NilError(t, validate.MatchAllString("A1", validate.NameRegex))
	err := validate.MatchAllString("1abc", validate.NameRegex)
	assert.ErrorContains(t, err, "Must start with a letter")
}

func TestRegexVarsExist(t *testing.T) {
	assert.Assert(t, len(validate.NameRegex) > 0)
	assert.Assert(t, len(validate.DescRegex) > 0)
	assert.Assert(t, len(validate.TagRegex) > 0)
}
