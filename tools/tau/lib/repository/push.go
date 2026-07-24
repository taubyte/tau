package repositoryLib

import (
	"github.com/taubyte/tau/tools/tau/config"
)

func (info *Info) Push(project config.Project, message, url string) (GitRepository, error) {
	repo, err := info.Open(project, url)
	if err != nil {
		return nil, err
	}

	err = repo.Commit(message, ".")
	if err != nil {
		return nil, err
	}

	err = repo.Push()
	if err != nil {
		return nil, err
	}

	return repo, nil
}
