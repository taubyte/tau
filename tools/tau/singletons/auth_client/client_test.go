package authClient_test

import (
	"testing"

	commonTest "github.com/taubyte/tau/tools/tau/common/test"
	authClient "github.com/taubyte/tau/tools/tau/singletons/auth_client"
	"github.com/taubyte/tau/tools/tau/singletons/config"
	"github.com/taubyte/tau/tools/tau/singletons/session"
	"gotest.tools/v3/assert"
)

func TestClient(t *testing.T) {
	t.Skip("Fix error: loading auth client failed with: no network selected")
	profiles := config.Profiles()
	testProfileName := "prof1"
	testProfile := config.Profile{
		Provider:    "github",
		Token:       commonTest.GitToken(t),
		Default:     false,
		NetworkType: "Remote",
		Network:     "sandbox.taubyte.com",
	}

	assert.NilError(t, profiles.Set(testProfileName, testProfile))
	assert.NilError(t, session.Set().ProfileName(testProfileName))

	_, err := authClient.Load()
	assert.NilError(t, err)
}
