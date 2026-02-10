package validate_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/validate"
	"gotest.tools/v3/assert"
)

func TestInList(t *testing.T) {
	list := []string{"a", "b", "c"}
	assert.Equal(t, validate.InList("", list), true)
	assert.Equal(t, validate.InList("a", list), true)
	assert.Equal(t, validate.InList("b", list), true)
	assert.Equal(t, validate.InList("d", list), false)
}

func TestMatchAllString(t *testing.T) {
	// exp[0] = error message, exp[1] = regex
	expressions := [][]string{
		{"must be letters", "^[a-z]+$"},
	}
	err := validate.MatchAllString("abc", expressions)
	assert.NilError(t, err)

	err = validate.MatchAllString("abc123", expressions)
	assert.ErrorContains(t, err, "must be letters")
}
