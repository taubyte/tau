package testutil

import (
	"github.com/taubyte/tau/tools/tau/constants"
)

// WithConfigPath sets constants.TauConfigFileName so the CLI uses the given config path for the test.
// Call the returned restore (e.g. t.Cleanup(restore)) to restore. For session, use session.LoadSessionInDir(dir) in the test.
func WithConfigPath(configPath string) (restore func()) {
	old := constants.TauConfigFileName
	constants.TauConfigFileName = configPath
	return func() { constants.TauConfigFileName = old }
}
