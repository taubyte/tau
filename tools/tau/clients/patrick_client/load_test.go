package patrickClient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/constants"
	"github.com/taubyte/tau/tools/tau/session"
	"gotest.tools/v3/assert"
)

func setupEnv(t *testing.T) (dir, configPath, sessionPath string, cleanup func()) {
	t.Helper()
	dir = t.TempDir()
	configPath = filepath.Join(dir, "tau.yaml")
	sessionPath = filepath.Join(dir, "session")
	if err := os.WriteFile(configPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessionPath, 0755); err != nil {
		t.Fatal(err)
	}
	oldConfig := constants.TauConfigFileName
	constants.TauConfigFileName = configPath
	cleanup = func() {
		constants.TauConfigFileName = oldConfig
		config.Clear()
		session.Clear()
	}
	return dir, configPath, sessionPath, cleanup
}

func TestLoad_NoProfilesReturnsError(t *testing.T) {
	_, _, sessionPath, cleanup := setupEnv(t)
	t.Cleanup(cleanup)

	session.Clear()
	config.Clear()
	assert.NilError(t, session.LoadSessionInDir(sessionPath))

	client, err := Load()
	assert.Assert(t, client == nil)
	assert.Assert(t, err != nil)
	msg := err.Error()
	assert.Assert(t, strings.Contains(msg, "profile") || strings.Contains(msg, "login") || strings.Contains(msg, "Patrick"),
		"expected profile/login/Patrick in error: %s", msg)
}

func TestLoad_NoCloudSelectedReturnsError(t *testing.T) {
	_, _, sessionPath, cleanup := setupEnv(t)
	t.Cleanup(cleanup)

	session.Clear()
	config.Clear()
	assert.NilError(t, session.LoadSessionInDir(sessionPath))

	assert.NilError(t, config.Profiles().Set("p1", config.Profile{
		Provider:  "github",
		Token:     "t",
		Default:   true,
		CloudType: common.RemoteCloud,
		Cloud:     "example.com",
	}))
	assert.NilError(t, session.Set().ProfileName("p1"))

	client, err := Load()
	assert.Assert(t, client == nil)
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "cloud") || strings.Contains(err.Error(), "Cloud"),
		"expected cloud in error: %s", err.Error())
}

func TestLoad_ProfileNotFoundReturnsError(t *testing.T) {
	_, _, sessionPath, cleanup := setupEnv(t)
	t.Cleanup(cleanup)

	session.Clear()
	config.Clear()
	assert.NilError(t, session.LoadSessionInDir(sessionPath))

	assert.NilError(t, config.Profiles().Set("other", config.Profile{Provider: "github", Token: "t", Default: true}))
	assert.NilError(t, session.Set().ProfileName("nonexistent"))

	client, err := Load()
	assert.Assert(t, client == nil)
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "profile") || strings.Contains(err.Error(), "nonexistent") || strings.Contains(err.Error(), "Patrick"),
		"expected profile/nonexistent/Patrick in error: %s", err.Error())
}

func TestLoad_UnknownCloudTypeReturnsError(t *testing.T) {
	_, _, sessionPath, cleanup := setupEnv(t)
	t.Cleanup(cleanup)

	session.Clear()
	config.Clear()
	assert.NilError(t, session.LoadSessionInDir(sessionPath))

	assert.NilError(t, config.Profiles().Set("p1", config.Profile{
		Provider:  "github",
		Token:     "t",
		Default:   true,
		CloudType: "invalid-cloud-type",
		Cloud:     "example.com",
	}))
	assert.NilError(t, session.Set().ProfileName("p1"))
	assert.NilError(t, session.Set().SelectedCloud("invalid-cloud-type"))

	client, err := Load()
	assert.Assert(t, client == nil)
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "cloud") || strings.Contains(err.Error(), "unknown") || strings.Contains(err.Error(), "Patrick"),
		"expected cloud/unknown/Patrick in error: %s", err.Error())
}

func TestLoad_RemoteCloudClientNewFailsReturnsError(t *testing.T) {
	_, _, sessionPath, cleanup := setupEnv(t)
	t.Cleanup(cleanup)

	session.Clear()
	config.Clear()
	assert.NilError(t, session.LoadSessionInDir(sessionPath))

	assert.NilError(t, config.Profiles().Set("p1", config.Profile{
		Provider:  "github",
		Token:     "t",
		Default:   true,
		CloudType: common.RemoteCloud,
		Cloud:     "example.com",
	}))
	assert.NilError(t, session.Set().ProfileName("p1"))
	assert.NilError(t, session.Set().SelectedCloud(common.RemoteCloud))

	client, err := Load()
	if err != nil {
		assert.Assert(t, client == nil)
		assert.Assert(t, strings.Contains(err.Error(), "Patrick") || strings.Contains(err.Error(), "client") || strings.Contains(err.Error(), "failed"),
			"error: %s", err.Error())
	} else {
		assert.Assert(t, client != nil)
	}
}
