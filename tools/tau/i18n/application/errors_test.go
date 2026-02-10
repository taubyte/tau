package applicationI18n_test

import (
	"errors"
	"testing"

	applicationI18n "github.com/taubyte/tau/tools/tau/i18n/application"
	"gotest.tools/v3/assert"
)

func TestSelectPromptFailed(t *testing.T) {
	err := applicationI18n.SelectPromptFailed(errors.New("bad"))
	assert.ErrorContains(t, err, "application prompt failed")
}
