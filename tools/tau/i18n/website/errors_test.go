package websiteI18n_test

import (
	"errors"
	"testing"

	websiteI18n "github.com/taubyte/tau/tools/tau/i18n/website"
	"gotest.tools/v3/assert"
)

func TestSelectPromptFailed(t *testing.T) {
	err := websiteI18n.SelectPromptFailed(errors.New("bad"))
	assert.ErrorContains(t, err, "website prompt failed")
}

func TestErrorAlreadyCloned(t *testing.T) {
	assert.Assert(t, websiteI18n.ErrorAlreadyCloned != nil)
	assert.ErrorContains(t, websiteI18n.ErrorAlreadyCloned, "already cloned")
}
