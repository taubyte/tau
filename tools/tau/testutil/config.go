package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/tools/tau/constants"
)

// WithConfigPath sets constants.TauConfigFileName so the CLI uses the given config path for the test.
// Call the returned restore (e.g. t.Cleanup(restore)) to restore. For session, use session.LoadSessionInDir(dir) in the test.
func WithConfigPath(configPath string) (restore func()) {
	old := constants.TauConfigFileName
	constants.TauConfigFileName = configPath
	return func() { constants.TauConfigFileName = old }
}

// RunWithIsolatedConfig runs a package's tests against a config of their own.
//
// A TEST MUST NEVER READ OR WRITE THE DEVELOPER'S REAL CONFIG, and several did:
// anything that reaches config.getOrCreateConfig — which is most of the CLI,
// since building the command tree resolves the selected project — loaded
// constants.TauConfigFileName as it stood, i.e. the one under the user's home.
// On a machine where that path was taken it panicked, and on every other machine
// it silently edited the user's projects while the suite passed.
//
// It is a TestMain rather than a helper each test calls because the damage is
// done by packages that never mention config at all. One line per package:
//
//	func TestMain(m *testing.M) { os.Exit(testutil.RunWithIsolatedConfig(m)) }
func RunWithIsolatedConfig(m *testing.M) int {
	dir, err := os.MkdirTemp("", "tau-test-config-")
	if err != nil {
		panic("isolating the test config failed with: " + err.Error())
	}
	defer os.RemoveAll(dir)

	restore := WithConfigPath(filepath.Join(dir, ".tau", "config.yaml"))
	defer restore()

	return m.Run()
}
