package projectLib

import (
	"github.com/taubyte/tau/pkg/git"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	"github.com/taubyte/tau/tools/tau/singletons/config"
)

func (h *repositoryHandler) openOrCloneProject(profile config.Profile, tauProject config.Project, embedToken bool) error {
	project, err := projectByName(h.projectName)
	if err != nil {
		return err
	}

	repoData, err := project.Repositories()
	if err != nil {
		return projectI18n.GettingRepositoryURLsFailed(h.projectName, err)
	}

	configUrl := git.URL(CleanGitURL(repoData.Configuration.Url))
	codeUrl := git.URL(CleanGitURL(repoData.Code.Url))

	var tokenOption git.Option
	if embedToken {
		tokenOption = git.EmbeddedToken(profile.Token)
	} else {
		tokenOption = git.Token(profile.Token)
	}

	h.config, err = h.openOrClone(profile, tauProject.ConfigLoc(), configUrl, tokenOption)
	if err != nil {
		return err
	}

	h.code, err = h.openOrClone(profile, tauProject.CodeLoc(), codeUrl, tokenOption)
	if err != nil {
		return err
	}

	return nil
}
