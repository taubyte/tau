package env

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/taubyte/tau/tools/tau/constants"
	envI18n "github.com/taubyte/tau/tools/tau/i18n/env"
	"github.com/taubyte/tau/tools/tau/singletons/config"
	"github.com/taubyte/tau/tools/tau/singletons/session"
	"github.com/urfave/cli/v2"
)

func SetSelectedProject(c *cli.Context, projectName string) error {
	if justDisplayExport(c, constants.CurrentProjectEnvVarName, projectName) {
		return nil
	}

	return session.Set().SelectedProject(projectName)
}

func GetSelectedProject() (string, error) {
	projectName, isSet := LookupEnv(constants.CurrentProjectEnvVarName)
	if isSet && len(projectName) > 0 {
		return projectName, nil
	}

	// Try to get project from current session
	projectName, exist := session.Get().SelectedProject()
	if exist && len(projectName) > 0 {
		return projectName, nil
	}

	// Try to get project from cwd
	projectName, exist = projectFromCwd()
	if exist && len(projectName) > 0 {
		return projectName, nil
	}

	return "", envI18n.ErrorProjectNotFound
}

func projectFromCwd() (projectName string, exist bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}

	for name, project := range config.Projects().List() {
		if strings.HasPrefix(cwd, filepath.Clean(project.Location)) {
			return name, true
		}
	}

	return "", false
}
