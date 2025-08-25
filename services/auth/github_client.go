package auth

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/go-github/v71/github"
	cu "github.com/taubyte/tau/services/auth/crypto"
	"golang.org/x/oauth2"
)

// GitHubClient defines the interface for GitHub operations
type GitHubClient interface {
	Cur() *github.Repository
	Me() *github.User
	GetByID(id string) error
	GetCurrentRepository() (*github.Repository, error)
	CreateRepository(name *string, description *string, private *bool) error
	CreateDeployKey(name *string, key *string) error
	CreatePushHook(name *string, url *string, devMode bool) (int64, string, error)
	ListMyRepos() map[string]RepositoryBasicInfo
	ShortRepositoryInfo(id string) RepositoryShortInfo
}

type githubClient struct {
	*github.Client
	Token              string
	ctx                context.Context
	user               *github.User
	current_repository *github.Repository
}

type RepositoryListOptions github.RepositoryListOptions

// RepositoryShortInfo represents basic repository information
type RepositoryShortInfo struct {
	Name     string `json:"name"`
	FullName string `json:"fullname"`
	URL      string `json:"url"`
	ID       string `json:"id"`
	Error    string `json:"error,omitempty"`
}

// RepositoryBasicInfo represents basic repository information for listing
type RepositoryBasicInfo struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func NewGitHubClient(ctx context.Context, token string) (GitHubClient, error) {

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	user, _, err := client.Users.Get(ctx, "")
	if err != nil {
		return nil, err
	}

	return &githubClient{client, token, ctx, user, nil}, nil
}

func (client *githubClient) Cur() *github.Repository {
	return client.current_repository
}

func (client *githubClient) Me() *github.User {
	return client.user
}

func (client *githubClient) GetByID(id string) error {
	_id, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return err
	}
	client.current_repository, _, err = client.Repositories.GetByID(client.ctx, _id)
	return err
}

func (client *githubClient) GetCurrentRepository() (*github.Repository, error) {
	if client.current_repository == nil {
		return nil, errors.New("client has no current repository")
	}

	return client.current_repository, nil
}

func (client *githubClient) CreateRepository(name *string, description *string, private *bool) (err error) {
	client.current_repository, _, err = client.Repositories.Create(client.ctx, "", &github.Repository{
		Name:        name,
		Private:     private,
		Description: description,
	})

	return
}

func (client *githubClient) CreateDeployKey(name *string, key *string) error {
	if client.current_repository == nil {
		// TODO: Make this a standard error
		return errors.New("no repository selected")
	}

	_, _, err := client.Repositories.CreateKey(client.ctx, *(client.user.Login), *(client.current_repository.Name), &github.Key{
		Title: name,
		Key:   key,
	})

	return err
}

func (client *githubClient) CreatePushHook(name *string, url *string, devMode bool) (int64, string, error) {
	if client.current_repository == nil {
		return 0, "", errors.New("no repository selected")
	}

	secret, err := cu.GenerateSecretString()
	if err != nil {
		return 0, "", err
	}

	// Don't create hooks in devMode as we are faking pushes
	if devMode {
		return 1, secret, nil
	}

	hk, _, err := client.Repositories.CreateHook(client.ctx, *(client.user.Login), *(client.current_repository.Name), &github.Hook{
		Events: []string{
			"push",
		},
		Config: &github.HookConfig{
			ContentType: github.Ptr("json"),
			Secret:      github.Ptr(secret),
			InsecureSSL: github.Ptr("0"),
			URL:         url,
		},
		Active: github.Ptr(true),
	})

	if err != nil {
		return 0, "", err
	}

	return *(hk.ID), secret, err
}

func (client *githubClient) ListMyRepos() map[string]RepositoryBasicInfo {
	repos := make(map[string]RepositoryBasicInfo)
	for i := 1; ; i++ {
		rlo := github.RepositoryListByAuthenticatedUserOptions{ListOptions: github.ListOptions{Page: i, PerPage: 100}, Sort: "created"}

		_repos, _, err := client.Repositories.ListByAuthenticatedUser(client.ctx, &rlo)
		// TODO: Simplify this logic
		if err == nil && len(_repos) > 0 {
			for _, v := range _repos {
				repos[fmt.Sprintf("%d", *(v.ID))] = RepositoryBasicInfo{
					Name: *(v.FullName),
					URL:  *(v.URL),
				}
			}
		} else {
			break
		}
	}
	return repos
}

func (client *githubClient) ShortRepositoryInfo(id string) RepositoryShortInfo {
	_id, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return RepositoryShortInfo{
			Error: "incorrect repository ID",
		}
	}

	_repoInfo, _, err := client.Repositories.GetByID(client.ctx, _id)
	if err != nil {
		return RepositoryShortInfo{
			Error: fmt.Sprintf("error: %s", err),
		}
	}

	return RepositoryShortInfo{
		Name:     *(_repoInfo.Name),
		FullName: *(_repoInfo.FullName),
		URL:      *(_repoInfo.URL),
		ID:       id,
	}
}
