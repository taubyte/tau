package promptsI18n_test

import (
	"testing"

	promptsI18n "github.com/taubyte/tau/tools/tau/i18n/prompts"
	"gotest.tools/v3/assert"
)

func TestInvalidType(t *testing.T) {
	err := promptsI18n.InvalidType("bad", []string{"a", "b"})
	assert.ErrorContains(t, err, "invalid type")
	assert.ErrorContains(t, err, "bad")
	assert.ErrorContains(t, err, "a")
}
