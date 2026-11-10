package repositoryLib

import (
	"errors"

	gogit "github.com/go-git/go-git/v5"
	"github.com/taubyte/tau/tools/tau/config"
)

// Push commits and pushes, and says whether anything reached the remote.
func (info *Info) Push(project config.Project, message, url string) (bool, error) {
	repo, err := info.Open(project, url)
	if err != nil {
		return false, err
	}

	// nothing to commit: push what is already committed
	err = repo.Commit(message, ".")
	if err != nil && !errors.Is(err, gogit.ErrEmptyCommit) {
		return false, err
	}

	return repo.PushNew()
}
