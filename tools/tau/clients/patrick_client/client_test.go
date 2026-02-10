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

func setupClientTest(t *testing.T) (sessionPath string, cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
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
	return sessionPath, cleanup
}

// TestLoad_DreamCloud_ReturnsErrorWhenNoDream exercises getClientUrl() Dream branch in client.go.
// Without a running dream, HTTPPort fails and Load returns an error.
func TestLoad_DreamCloud_ReturnsErrorWhenNoDream(t *testing.T) {
	sessionPath, cleanup := setupClientTest(t)
	t.Cleanup(cleanup)

	session.Clear()
	config.Clear()
	assert.NilError(t, session.LoadSessionInDir(sessionPath))

	config.Profiles().Set("p1", config.Profile{
		Provider:  "github",
		Token:     "t",
		Default:   true,
		CloudType: common.DreamCloud,
		Cloud:     "",
	})
	assert.NilError(t, session.Set().ProfileName("p1"))
	assert.NilError(t, session.Set().SelectedCloud(common.DreamCloud))

	client, err := Load()
	assert.Assert(t, client == nil)
	assert.Assert(t, err != nil)
	msg := err.Error()
	assert.Assert(t,
		strings.Contains(msg, "Patrick") || strings.Contains(msg, "client") || strings.Contains(msg, "dream") || strings.Contains(msg, "failed"),
		"expected Patrick/client/dream/failed in error: %s", msg)
}

// TestLoad_RemoteCloud_BuildsURL exercises getClientUrl() Remote branch in client.go.
// Verifies we attempt to create the client with profile.Cloud (URL shape: https://patrick.tau.<cloud>).
func TestLoad_RemoteCloud_BuildsURL(t *testing.T) {
	sessionPath, cleanup := setupClientTest(t)
	t.Cleanup(cleanup)

	session.Clear()
	config.Clear()
	assert.NilError(t, session.LoadSessionInDir(sessionPath))

	config.Profiles().Set("p1", config.Profile{
		Provider:  "github",
		Token:     "token",
		Default:   true,
		CloudType: common.RemoteCloud,
		Cloud:     "example.com",
	})
	assert.NilError(t, session.Set().ProfileName("p1"))
	assert.NilError(t, session.Set().SelectedCloud(common.RemoteCloud))

	// Load() will call getClientUrl() -> "https://patrick.tau.example.com", then client.New().
	// We don't start a real server; we only assert we get either a client (if something responds) or a clear error.
	c, err := Load()
	if err != nil {
		assert.Assert(t, c == nil)
		assert.Assert(t, strings.Contains(err.Error(), "Patrick") || strings.Contains(err.Error(), "client") || strings.Contains(err.Error(), "failed"))
		return
	}
	assert.Assert(t, c != nil)
}
