package libraryI18n_test

import (
	"errors"
	"testing"

	libraryI18n "github.com/taubyte/tau/tools/tau/i18n/library"
	"gotest.tools/v3/assert"
)

func TestSelectPromptFailed(t *testing.T) {
	err := libraryI18n.SelectPromptFailed(errors.New("bad"))
	assert.ErrorContains(t, err, "library prompt failed")
}

func TestErrorAlreadyCloned(t *testing.T) {
	assert.Assert(t, libraryI18n.ErrorAlreadyCloned != nil)
	assert.ErrorContains(t, libraryI18n.ErrorAlreadyCloned, "already cloned")
}
