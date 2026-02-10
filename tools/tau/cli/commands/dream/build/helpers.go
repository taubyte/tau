package build

import (
	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/tools/tau/config"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
)

type buildHelper struct {
	project       project.Project
	projectConfig config.Project
	currentBranch string
	selectedApp   string
}

func initBuild() (*buildHelper, error) {
	var err error
	helper := &buildHelper{}

	helper.project, err = projectLib.SelectedProjectInterface()
	if err != nil {
		return nil, err
	}

	helper.projectConfig, err = projectLib.SelectedProjectConfig()
	if err != nil {
		return nil, err
	}

	h := projectLib.Repository(helper.project.Get().Name())
	projectRepositories, err := h.Open()
	if err != nil {
		return nil, err
	}

	helper.currentBranch, err = projectRepositories.CurrentBranch()
	if err != nil {
		return nil, err
	}

	helper.selectedApp, _ = config.GetSelectedApplication()
	return helper, nil
}
