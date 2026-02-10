package databaseI18n_test

import (
	"errors"
	"testing"

	databaseI18n "github.com/taubyte/tau/tools/tau/i18n/database"
	"gotest.tools/v3/assert"
)

func TestSelectPromptFailed(t *testing.T) {
	err := databaseI18n.SelectPromptFailed(errors.New("bad"))
	assert.ErrorContains(t, err, "selecting a database prompt failed")
	assert.ErrorContains(t, err, "bad")
}
