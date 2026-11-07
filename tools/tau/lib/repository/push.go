package repositoryLib

import (
	"errors"

	gogit "github.com/go-git/go-git/v5"
	"github.com/taubyte/tau/tools/tau/config"
)

func (info *Info) Push(project config.Project, message, url string) (GitRepository, error) {
	repo, err := info.Open(project, url)
	if err != nil {
		return nil, err
	}

	// nothing to commit: push what is already committed
	err = repo.Commit(message, ".")
	if err != nil && !errors.Is(err, gogit.ErrEmptyCommit) {
		return nil, err
	}

	err = repo.Push()
	if err != nil {
		return nil, err
	}

	return repo, nil
}
