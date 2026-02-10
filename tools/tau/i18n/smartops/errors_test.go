package smartopsI18n_test

import (
	"errors"
	"testing"

	smartopsI18n "github.com/taubyte/tau/tools/tau/i18n/smartops"
	"gotest.tools/v3/assert"
)

func TestSelectPromptFailed(t *testing.T) {
	err := smartopsI18n.SelectPromptFailed(errors.New("bad"))
	assert.ErrorContains(t, err, "smartops prompt failed")
}
