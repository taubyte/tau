package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/avast/retry-go/v4"
	"github.com/google/go-github/v71/github"
	"github.com/taubyte/tau/clients/http/auth/git/common"
)

// Git returns a git client based on the current git provider
// Currently only github is supported
func (c *Client) Git() common.Client {
	return c.gitClient
}

// CreateRepository creates a new repository on the git provider. Returns the repository id and an error
func (c *Client) CreateRepository(name string, description string, private bool) (id string, err error) {
	repo, err := createRepository(c.http.Context(), c.Git(), name, description, private,
		retry.Attempts(CreateRepositoryRetryAttempts),
		retry.Delay(CreateRepositoryRetryDelay),
	)
	if err != nil {
		return "", err
	}

	return repo.Get().ID(), nil
}

// createRepository retries a failed creation, but creating is not idempotent:
// GitHub can answer 5xx after making the repository, and asking again then only
// gets "name already exists". So after a server error it looks for the
// repository first. Without a server error a taken name stays an error, so a
// repository that was already there is never adopted.
func createRepository(ctx context.Context, g common.Client, name, description string, private bool, opts ...retry.Option) (common.Repository, error) {
	var (
		repo        common.Repository
		serverError bool
	)
	find := func() common.Repository {
		gh, err := g.GithubTODO()
		if err != nil {
			return nil
		}
		me, _, err := gh.Users.Get(ctx, "")
		if err != nil {
			return nil
		}
		found, err := g.GetByName(me.GetLogin(), name)
		if err != nil {
			return nil
		}
		return found
	}

	err := retry.Do(func() error {
		if serverError {
			if repo = find(); repo != nil {
				return nil
			}
		}

		var callErr error
		repo, callErr = g.CreateRepository(name, description, private)
		var response *github.ErrorResponse
		if errors.As(callErr, &response) && response.Response != nil && response.Response.StatusCode >= http.StatusInternalServerError {
			serverError = true
		}
		return callErr
	}, opts...)
	if err != nil && serverError {
		if repo = find(); repo != nil {
			return repo, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return repo, nil
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
		return nil, fmt.Errorf("invalid git fullName: `%s`, expected `owner/name`", fullName)
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
