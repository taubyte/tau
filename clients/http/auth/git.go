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

// RepositoryEmpty reports whether a repository has no commits yet.
func (c *Client) RepositoryEmpty(owner, name string) (bool, error) {
	return repositoryEmpty(c.http.Context(), c.Git(), owner, name)
}

func repositoryEmpty(ctx context.Context, g common.Client, owner, name string) (bool, error) {
	gh, err := g.GithubTODO()
	if err != nil {
		return false, err
	}
	_, resp, err := gh.Repositories.ListCommits(ctx, owner, name, &github.CommitsListOptions{ListOptions: github.ListOptions{PerPage: 1}})
	// GitHub answers 409 Conflict for a repository with no commits.
	if resp != nil && resp.StatusCode == http.StatusConflict {
		return true, nil
	}
	return false, err
}

// EmptyRepository returns the id of the caller's repository called name when it
// has no commits: all an interrupted `tau new project` leaves behind, so using
// it cannot overwrite anything. Otherwise, or on any error, it returns "".
func (c *Client) EmptyRepository(name string) string {
	return emptyRepository(c.http.Context(), c.Git(), name)
}

func emptyRepository(ctx context.Context, g common.Client, name string) string {
	gh, err := g.GithubTODO()
	if err != nil {
		return ""
	}
	me, _, err := gh.Users.Get(ctx, "")
	if err != nil {
		return ""
	}
	found, err := g.GetByName(me.GetLogin(), name)
	if err != nil {
		return ""
	}
	if empty, err := repositoryEmpty(ctx, g, me.GetLogin(), name); err != nil || !empty {
		return ""
	}
	return found.Get().ID()
}

// RepositoryHasCommits reports whether the caller has a repository called name
// that was seen to hold commits. On any error it returns false.
func (c *Client) RepositoryHasCommits(name string) bool {
	return repositoryHasCommits(c.http.Context(), c.Git(), name)
}

func repositoryHasCommits(ctx context.Context, g common.Client, name string) bool {
	gh, err := g.GithubTODO()
	if err != nil {
		return false
	}
	me, _, err := gh.Users.Get(ctx, "")
	if err != nil {
		return false
	}
	empty, err := repositoryEmpty(ctx, g, me.GetLogin(), name)
	return err == nil && !empty
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
