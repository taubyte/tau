package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/taubyte/tau/tools/tau/i18n/selection"
	"github.com/taubyte/tau/tools/tau/session"
)

func projectFromCwd() (projectName string, exist bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for name, project := range Projects().List() {
		if strings.HasPrefix(cwd, filepath.Clean(project.Location)) {
			return name, true
		}
	}
	return "", false
}

// GetSelectedProject returns selected project: session first, then projectFromCwd.
func GetSelectedProject() (string, error) {
	projectName, exist := session.Get().SelectedProject()
	if exist && projectName != "" {
		return projectName, nil
	}
	projectName, exist = projectFromCwd()
	if exist && projectName != "" {
		return projectName, nil
	}
	return "", selection.ErrorProjectNotFound
}

func profileFromProject() (name string, exist bool) {
	projectName, err := GetSelectedProject()
	if err != nil {
		return "", false
	}
	project, err := Projects().Get(projectName)
	if err != nil {
		return "", false
	}
	return project.DefaultProfile, true
}

// GetSelectedUser returns selected profile: session first, then profileFromProject, then config default.
func GetSelectedUser() (string, error) {
	profileName, exist := session.Get().ProfileName()
	if exist && profileName != "" {
		return profileName, nil
	}
	profileName, exist = profileFromProject()
	if exist && profileName != "" {
		return profileName, nil
	}
	for name, profile := range Profiles().List(false) {
		if profile.Default {
			return name, nil
		}
	}
	return "", selection.ErrorUserNotFound
}

// GetSelectedApplication returns selected application from session only.
func GetSelectedApplication() (string, bool) {
	return session.Get().SelectedApplication()
}
