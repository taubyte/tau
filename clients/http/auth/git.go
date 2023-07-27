package client

import (
	"fmt"
	"strings"

	"github.com/avast/retry-go"
	git "github.com/taubyte/tau/clients/http/auth/git"
	"github.com/taubyte/tau/clients/http/auth/git/common"
)

// Git returns a git client based on the current git provider
// Currently only github is supported
func (c *Client) Git() common.Client {
	if c.gitClient == nil {
		c.gitClient = git.New(c.ctx, c.provider, c.token)
	}

	return c.gitClient
}

// CreateRepository creates a new repository on the git provider. Returns the repository id and an error
func (c *Client) CreateRepository(name string, description string, private bool) (id string, err error) {
	var repo common.Repository
	err = retry.Do(
		func() (callErr error) {
			repo, callErr = c.Git().CreateRepository(name, description, private)
			return
		},
		retry.Attempts(CreateRepositoryRetryAttempts),
		retry.Delay(CreateRepositoryRetryDelay),
	)
	if err != nil {
		return "", err
	}

	return repo.Get().ID(), nil
}

// GetRepositoryById returns a common Repository based on the id and an error
func (c *Client) GetRepositoryById(repoId string) (common.Repository, error) {
	repo, err := c.Git().GetByID(repoId)
	if err != nil {
		return nil, err
	}

	newId := repo.Get().ID()
	if newId != repoId {
		return nil, fmt.Errorf("repository id `%s` does not match received id `%s`", repoId, newId)
	}

	return repo, nil
}

// GetRepositoryByName returns a common Repository based on the name and an error
func (c *Client) GetRepositoryByName(fullName string) (common.Repository, error) {
	nameSplit := strings.Split(fullName, "/")
	if len(nameSplit) != 2 {
		return nil, fmt.Errorf("invalid git fullname: `%s`, expected `owner/name`", fullName)
	}

	owner := nameSplit[0]
	repoName := nameSplit[1]

	repo, err := c.Git().GetByName(owner, repoName)
	if err != nil {
		return nil, err
	}

	return repo, nil
}

// ListRepositories returns a list of common Repositories and an error
func (c *Client) ListRepositories() ([]common.Repository, error) {
	return c.Git().ListRepositories()
}
