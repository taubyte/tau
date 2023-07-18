package git

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"

	"bitbucket.org/taubyte/go-auth-http/git/common"
	"github.com/google/go-github/v32/github"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

type client struct {
	ctx context.Context
	*github.Client
}

// New creates a new github client
func New(ctx context.Context, token string) common.Client {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	return &client{
		ctx:    ctx,
		Client: github.NewClient(tc),
	}
}

// GithubTODO is a temporary function to extract the inner github client
func (c *client) GithubTODO() (*github.Client, error) {
	return c.Client, nil
}

// CreateRepository creates a new github repository and returns a common.Repository and an error
// Note: currently description is unused and should be written into the config.yaml of the configuration repository
func (c *client) CreateRepository(name string, description string, private bool) (common.Repository, error) {
	newRepo := &github.Repository{
		Name:        &name,
		Description: &description,
		Private:     &private,
	}
	repo, _, err := c.Repositories.Create(c.ctx, "", newRepo)
	if err != nil {
		return nil, err
	}

	return &repository{repo}, nil
}

// GetByID gets a repository by its id returning a common.Repository and an error
func (c *client) GetByID(id string) (common.Repository, error) {
	_id, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing id `%s` failed with: %s", id, err)
	}

	repo, _, err := c.Repositories.GetByID(c.ctx, _id)
	if err != nil {
		return nil, err
	}

	return &repository{repo}, nil
}

// GetByName gets a repository by its name returning a common.Repository and an error
func (c *client) GetByName(owner, name string) (common.Repository, error) {
	repo, _, err := c.Repositories.Get(c.ctx, owner, name)
	if err != nil {
		return nil, err
	}

	return &repository{repo}, nil
}

// ListRepositories lists all repositories of the current user
// returning a slice of common.Repository and an error
func (c *client) ListRepositories() ([]common.Repository, error) {
	opt := &github.RepositoryListOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}

	var allRepos []*github.Repository
	for {
		repos, resp, err := c.Repositories.List(c.ctx, "", opt)
		if err != nil {
			return nil, err
		}
		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	ret := make([]common.Repository, len(allRepos))
	for i, repo := range allRepos {
		ret[i] = &repository{repo}
	}

	return ret, nil
}

// ReadConfig reads the config.yaml file from the repository
// returning a common.ProjectConfig and an error
func (c *client) ReadConfig(owner, repo string) (*common.ProjectConfig, error) {
	content, _, _, err := c.Repositories.GetContents(
		c.ctx,
		owner,
		repo,
		"config.yaml",
		nil,
	)
	if err != nil {
		return nil, err
	}

	ret, err := base64.StdEncoding.DecodeString(*content.Content)
	if err != nil {
		return nil, err
	}

	config := &common.ProjectConfig{}
	err = yaml.Unmarshal(ret, config)
	if err != nil {
		return nil, fmt.Errorf("un-marshalling config.yaml failed with: %s", err)
	}

	return config, nil
}
