package projectLib

import (
	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/i18n"
)

func SelectedProjectInterface() (project.Project, error) {
	configProject, err := SelectedProjectConfig()
	if err != nil {
		return nil, err
	}

	project, err := configProject.Interface()
	if err != nil {
		i18n.Help().BeSureToCloneProject()
		return nil, err
	}

	return project, nil
}

func SelectedProjectConfig() (configProject config.Project, err error) {
	selectedProject, err := config.GetSelectedProject()
	if err != nil {
		i18n.Help().BeSureToSelectProject()
		return
	}

	configProject, err = config.Projects().Get(selectedProject)
	if err != nil {
		i18n.Help().BeSureToCloneProject()
	}

	return
}

func ConfirmSelectedProject() error {
	_, err := config.GetSelectedProject()
	if err != nil {
		i18n.Help().BeSureToSelectProject()
	}

	return err
}
