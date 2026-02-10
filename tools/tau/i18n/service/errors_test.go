package serviceI18n_test

import (
	"errors"
	"testing"

	serviceI18n "github.com/taubyte/tau/tools/tau/i18n/service"
	"gotest.tools/v3/assert"
)

func TestSelectPromptFailed(t *testing.T) {
	err := serviceI18n.SelectPromptFailed(errors.New("bad"))
	assert.ErrorContains(t, err, "service prompt failed")
}
