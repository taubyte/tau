package projectLib

import (
	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/i18n"
)

// SelectedProjectConfig is the ~/tau.yaml entry of the selected project (name,
// location, profile). The project's *configuration* is read through tcc, not
// from here.
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
