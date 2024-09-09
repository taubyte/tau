package projectLib

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/singletons"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/singletons/config"
)

func (h *repositoryHandler) Clone(tauProject config.Project, embedToken bool) (ProjectRepository, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	if len(tauProject.Location) == 0 {
		tauProject.Location = path.Join(cwd, tauProject.Name)
	} else if !filepath.IsAbs(tauProject.Location) {
		tauProject.Location = path.Join(cwd, tauProject.Location)
	}

	// Check if user has already defined project name in given location
	if !strings.HasSuffix(strings.ToLower(tauProject.Location), strings.ToLower(tauProject.Name)) {
		tauProject.Location = path.Join(tauProject.Location, tauProject.Name)
	}

	profile, err := loginLib.GetSelectedProfile()
	if err != nil {
		return nil, err
	}
	if len(tauProject.DefaultProfile) == 0 {
		tauProject.DefaultProfile = profile.Name()
	}

	// check if the project is configured, if not delete it from config and continue
	testProject, err := config.Projects().Get(h.projectName)
	if err == nil {
		_, configErr := os.Stat(testProject.ConfigLoc())
		_, codeErr := os.Stat(testProject.CodeLoc())
		if configErr != nil || codeErr != nil {
			err = config.Projects().Delete(h.projectName)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, singletonsI18n.ProjectAlreadyCloned(h.projectName, testProject.Location)
		}
	}

	err = h.openOrCloneProject(profile, tauProject, embedToken)
	if err != nil {
		return nil, err
	}

	err = config.Projects().Set(h.projectName, tauProject)
	if err != nil {
		return nil, err
	}

	return h, nil
}
