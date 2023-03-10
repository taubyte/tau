package env

import (
	"github.com/taubyte/tau/constants"
	"github.com/taubyte/tau/singletons/session"
	"github.com/urfave/cli/v2"
)

func SetSelectedApplication(c *cli.Context, name string) error {
	if justDisplayExport(c, constants.CurrentApplicationEnvVarName, name) {
		return nil
	}

	return session.Set().SelectedApplication(name)
}

// Only returns an error if not found
func GetSelectedApplication() (name string, exist bool) {
	name, isSet := LookupEnv(constants.CurrentApplicationEnvVarName)
	if isSet == true && len(name) > 0 {
		return
	}

	// Try to get app from current session
	return session.Get().SelectedApplication()
}
