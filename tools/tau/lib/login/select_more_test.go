package loginLib_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/tools/tau/config"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func loginEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	config.Clear()
	session.Clear()
	t.Cleanup(func() { config.Clear(); session.Clear() })
	t.Cleanup(testutil.WithConfigPath(filepath.Join(dir, "tau.yaml")))
	sessDir := filepath.Join(dir, "session")
	assert.NilError(t, os.MkdirAll(sessDir, 0o755))
	assert.NilError(t, session.LoadSessionInDir(sessDir))

	assert.NilError(t, config.Profiles().Set("a", config.Profile{Provider: "github", Token: "t1", Default: true, CloudType: "test", Cloud: "net"}))
	assert.NilError(t, config.Profiles().Set("b", config.Profile{Provider: "github", Token: "t2"}))
}

func TestGetProfiles(t *testing.T) {
	loginEnv(t)
	def, all, err := loginLib.GetProfiles()
	assert.NilError(t, err)
	assert.Equal(t, def, "a")
	assert.Equal(t, len(all), 2)
}

// Select sets the profile's cloud into the session and makes it the active
// profile, and (with setDefault) moves the default flag.
func TestSelectProfile(t *testing.T) {
	loginEnv(t)
	assert.NilError(t, loginLib.Select(nil, "b", true))

	name, ok := session.Get().ProfileName()
	assert.Assert(t, ok)
	assert.Equal(t, name, "b")

	def, _, _ := loginLib.GetProfiles()
	assert.Equal(t, def, "b") // default moved a -> b

	// GetSelectedProfile follows the session's selected user
	assert.NilError(t, session.Set().ProfileName("b"))
	// config.GetSelectedUser resolves the active profile; b is now selected
	prof, err := loginLib.GetSelectedProfile()
	assert.NilError(t, err)
	assert.Equal(t, prof.Token, "t2")
}
