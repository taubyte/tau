package functionI18n_test

import (
	"errors"
	"testing"

	functionI18n "github.com/taubyte/tau/tools/tau/i18n/function"
	"gotest.tools/v3/assert"
)

func TestSelectPromptFailed(t *testing.T) {
	err := functionI18n.SelectPromptFailed(errors.New("bad"))
	assert.ErrorContains(t, err, "function prompt failed")
}
