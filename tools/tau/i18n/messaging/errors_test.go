package messagingI18n_test

import (
	"errors"
	"testing"

	messagingI18n "github.com/taubyte/tau/tools/tau/i18n/messaging"
	"gotest.tools/v3/assert"
)

func TestSelectPromptFailed(t *testing.T) {
	err := messagingI18n.SelectPromptFailed(errors.New("bad"))
	assert.ErrorContains(t, err, "messaging prompt failed")
}
