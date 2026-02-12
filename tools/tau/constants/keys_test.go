package constants_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/constants"
	"gotest.tools/v3/assert"
)

func TestSessionKeys(t *testing.T) {
	assert.Equal(t, constants.KeyProfile, "profile")
	assert.Equal(t, constants.KeyProject, "project")
	assert.Equal(t, constants.KeyApplication, "application")
	assert.Equal(t, constants.KeySelectedCloud, "selected_cloud")
	assert.Equal(t, constants.KeyCustomCloudURL, "custom_cloud_url")
}
