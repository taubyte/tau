package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-github/v71/github"
	"github.com/taubyte/tau/pkg/git/repokey"
	cu "github.com/taubyte/tau/services/auth/crypto"
	"golang.org/x/oauth2"
)

// GitHubClient defines the interface for GitHub operations
type GitHubClient interface {
	Cur() *github.Repository
	Me() *github.User
	Get(key repokey.Key) error
	GetCurrentRepository() (*github.Repository, error)
	CreateRepository(name *string, description *string, private *bool) error
	CreateDeployKey(name *string, key *string) error
	CreatePushHook(name *string, url *string, devMode bool) (int64, string, error)
	ListMyRepos() map[string]RepositoryBasicInfo
	ShortRepositoryInfo(key repokey.Key) RepositoryShortInfo
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
	if token == "" {
		return &githubClient{Client: github.NewClient(http.DefaultClient), ctx: ctx}, nil
	}

	client := github.NewClient(oauth2.NewClient(ctx, oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)))

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

// Get fetches the repository a key names. It goes through GitHub's owner/name
// endpoint rather than the numeric-id one because the key is what we hold: the
// numeric id was the thing being removed, and keeping a lookup that needed it
// would have meant storing it after all.
func (client *githubClient) Get(key repokey.Key) error {
	owner, name, err := ownerName(key)
	if err != nil {
		return err
	}

	client.current_repository, _, err = client.Repositories.Get(client.ctx, owner, name)
	return err
}

// ownerName splits a key for GitHub's API, which takes exactly one owner and
// one name. A key may carry a deeper path — a GitLab subgroup does — and this
// client cannot address that, so it says so rather than silently joining the
// segments into a name no repository has.
func ownerName(key repokey.Key) (string, string, error) {
	if key.Host != "github.com" {
		return "", "", fmt.Errorf("repository `%s` is not on github.com; this client speaks to GitHub", key)
	}
	if key.Owner == "" || strings.Contains(key.Owner, "/") {
		return "", "", fmt.Errorf("repository `%s` does not have a GitHub owner/name shape", key)
	}
	return key.Owner, key.Name, nil
}

func (client *githubClient) GetCurrentRepository() (*github.Repository, error) {
	if client.current_repository == nil {
		return nil, errors.New("Client has no current repository")
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
		return errors.New("no repository selected")
	}

	owner, repo, err := client.currentOwnerRepo()
	if err != nil {
		return err
	}

	_, _, err = client.Repositories.CreateKey(client.ctx, owner, repo, &github.Key{
		Title: name,
		Key:   key,
	})

	return err
}

// currentOwnerRepo is the owner and name of the selected repository. The owner
// is the repository's, not the caller's: they differ whenever the repository
// belongs to an organization the caller is a member of.
func (client *githubClient) currentOwnerRepo() (string, string, error) {
	if client.current_repository == nil {
		return "", "", errors.New("no repository selected")
	}
	owner := client.current_repository.GetOwner().GetLogin()
	if owner == "" {
		return "", "", errors.New("repository owner unavailable")
	}
	name := client.current_repository.GetName()
	if name == "" {
		return "", "", errors.New("repository name unavailable")
	}
	return owner, name, nil
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

	owner, repo, err := client.currentOwnerRepo()
	if err != nil {
		return 0, "", err
	}

	hk, _, err := client.Repositories.CreateHook(client.ctx, owner, repo, &github.Hook{
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
		if err == nil && len(_repos) > 0 {
			for _, v := range _repos {
				// The clone URL is the identity, so the listing is keyed by
				// what a clone URL derives to and not by the id GitHub happens
				// to have given the repository.
				key, err := repokey.Parse(v.GetCloneURL())
				if err != nil {
					logger.Errorf("skipping repository `%s`: %s", v.GetFullName(), err)
					continue
				}
				repos[key.String()] = RepositoryBasicInfo{
					Name: v.GetFullName(),
					URL:  v.GetURL(),
				}
			}
		} else {
			break
		}
	}
	return repos
}

func (client *githubClient) ShortRepositoryInfo(key repokey.Key) RepositoryShortInfo {
	owner, name, err := ownerName(key)
	if err != nil {
		return RepositoryShortInfo{Error: err.Error()}
	}

	_repoInfo, _, err := client.Repositories.Get(client.ctx, owner, name)
	if err != nil {
		return RepositoryShortInfo{
			Error: fmt.Sprintf("Error %s", err),
		}
	}

	return RepositoryShortInfo{
		Name:     _repoInfo.GetName(),
		FullName: _repoInfo.GetFullName(),
		URL:      _repoInfo.GetURL(),
		ID:       key.String(),
	}
}
