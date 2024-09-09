package projectLib

import (
	"github.com/taubyte/tau/pkg/git"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/singletons/config"
)

func (h *repositoryHandler) Open() (ProjectRepository, error) {
	profile, err := loginLib.GetSelectedProfile()
	if err != nil {
		return nil, err
	}

	project, err := config.Projects().Get(h.projectName)
	if err != nil {
		return nil, err
	}

	h.config, err = h.openOrClone(profile, project.ConfigLoc(), git.Token(profile.Token))
	if err != nil {
		return nil, err
	}

	h.code, err = h.openOrClone(profile, project.CodeLoc(), git.Token(profile.Token))
	if err != nil {
		return nil, err
	}

	return h, nil
}
