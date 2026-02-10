package cloudI18n_test

import (
	"testing"

	cloudI18n "github.com/taubyte/tau/tools/tau/i18n/cloud"
	"gotest.tools/v3/assert"
)

func TestFlagError(t *testing.T) {
	err := cloudI18n.FlagError()
	assert.ErrorContains(t, err, "only set one flag")
}

func TestErrorUnknownCloud(t *testing.T) {
	err := cloudI18n.ErrorUnknownCloud("my-cloud")
	assert.ErrorContains(t, err, "unknown cloud")
	assert.ErrorContains(t, err, "my-cloud")
}
