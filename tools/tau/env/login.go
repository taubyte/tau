package env

import (
	"github.com/taubyte/tau/tools/tau/constants"
	envI18n "github.com/taubyte/tau/tools/tau/i18n/env"
	"github.com/taubyte/tau/tools/tau/singletons/config"
	"github.com/taubyte/tau/tools/tau/singletons/session"
	"github.com/urfave/cli/v2"
)

func SetSelectedUser(c *cli.Context, profileName string) error {
	if justDisplayExport(c, constants.CurrentSelectedProfileNameEnvVarName, profileName) {
		return nil
	}

	return session.Set().ProfileName(profileName)
}

func GetSelectedUser() (string, error) {
	profileName, isSet := LookupEnv(constants.CurrentSelectedProfileNameEnvVarName)
	if isSet && len(profileName) > 0 {
		return profileName, nil
	}

	// Try to get profile from current session
	profileName, exist := session.Get().ProfileName()
	if exist && len(profileName) > 0 {
		return profileName, nil
	}

	// Try to get profile from selected project
	profileName, exist = profileFromProject()
	if exist && len(profileName) > 0 {
		return profileName, nil
	}

	// Try to get default profile if env variable not set
	for _profileName, profile := range config.Profiles().List(false) {
		if profile.Default {
			profileName = _profileName

			break
		}
	}

	if len(profileName) == 0 {
		return "", envI18n.ErrorUserNotFound
	}

	return profileName, nil
}

func profileFromProject() (name string, exist bool) {
	projectName, err := GetSelectedProject()
	if err != nil {
		return "", false
	}

	project, err := config.Projects().Get(projectName)
	if err != nil {
		return "", false
	}

	return project.DefaultProfile, true
}
