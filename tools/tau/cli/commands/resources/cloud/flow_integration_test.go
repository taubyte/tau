//go:build dreaming

package cloud_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"

	commonIface "github.com/taubyte/tau/core/common"

	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
)

// TestCloudFlow_SelectNetworkWithDream runs an integration test: start dream on default port, then tau select cloud --universe <name>.
//
// Note: This test runs "select cloud" and "query project" in the same process via RunCLIWithDir (no subprocess).
// Session is set with LoadSessionInDir(dir/session), so discovery (ppid-based $TMPDIR/tau-<pid>) is never used.
func TestCloudFlow_SelectNetworkWithDream_Dreaming(t *testing.T) {
	dream.DreamApiPort = 41422
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	srv, err := api.New(m, nil)
	assert.NilError(t, err)
	srv.Server().Start()
	_, err = srv.Ready(10 * time.Second)
	assert.NilError(t, err)

	universeName := "select-network-test"
	u, err := m.New(dream.UniverseConfig{Name: universeName})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {},
			"auth": {},
		},
	})
	assert.NilError(t, err)

	dir := t.TempDir()
	cfg := `profiles:
  test:
    provider: github
    token: "123456"
    default: true
    type: dream
projects: {}
`
	_, _, err = testutil.RunCLIWithDir(t, cli.Run, dir, cfg,
		"login", "test", "-p", "github", "-t", "123456", "--color", "never",
	)
	assert.NilError(t, err)

	assert.NilError(t, session.LoadSessionInDir(filepath.Join(dir, "session")))

	_, _, err = testutil.RunCLIWithDir(t, cli.Run, dir, "",
		"select", "cloud", "--universe", universeName, "--color", "never",
	)
	assert.NilError(t, err)

	cloudType, ok := session.GetSelectedCloud()
	assert.Assert(t, ok, "selected cloud should be set")
	assert.Equal(t, cloudType, common.DreamCloud)

	cloudValue, ok := session.GetCustomCloudUrl()
	assert.Assert(t, ok, "cloud value (universe name) should be set")
	assert.Equal(t, cloudValue, universeName)

	// List projects to confirm the CLI uses the selected dream cloud. We expect login failure (invalid token); anything else (e.g. connection error) is a test failure.
	_, _, listErr := testutil.RunCLIWithDir(t, cli.Run, dir, "",
		"query", "project", "--list", "--color", "never",
	)
	assert.Assert(t, listErr != nil, "list projects should fail with login/auth (no valid token for dream)")
	errMsg := listErr.Error()
	loginFailure := strings.Contains(errMsg, "401") ||
		strings.Contains(errMsg, "Unauthorized") ||
		strings.Contains(errMsg, "invalid") && strings.Contains(errMsg, "token") ||
		strings.Contains(errMsg, "Have you logged in") ||
		strings.Contains(errMsg, "logged in")
	assert.Assert(t, loginFailure, "list projects must fail with login failure (expected), not: %v", listErr)

	config.Clear()
	session.Clear()
}
