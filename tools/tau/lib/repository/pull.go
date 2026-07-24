package repositoryLib

import (
	"github.com/taubyte/tau/tools/tau/config"
)

func (info *Info) Pull(project config.Project, url string) (GitRepository, error) {
	repo, err := info.Open(project, url)
	if err != nil {
		return nil, err
	}

	err = repo.Pull()
	if err != nil {
		return nil, err
	}

	return repo, nil
}
