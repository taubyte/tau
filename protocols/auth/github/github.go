package github

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/go-github/v32/github"
	cu "github.com/taubyte/odo/protocols/auth/crypto"
	"golang.org/x/oauth2"
)

type Client struct {
	*github.Client
	Token              string
	ctx                context.Context
	user               *github.User
	current_repository *github.Repository
}

type RepositoryListOptions github.RepositoryListOptions

func New(ctx context.Context, token string) (*Client, error) {

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	user, _, err := client.Users.Get(ctx, "")
	if err != nil {
		return nil, err
	}

	return &Client{client, token, ctx, user, nil}, nil
}

func (client *Client) Cur() *github.Repository {
	return client.current_repository
}

func (client *Client) Me() *github.User {
	return client.user
}

func (client *Client) GetByID(id string) error {
	_id, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return err
	}
	client.current_repository, _, err = client.Repositories.GetByID(client.ctx, _id)
	return err
}

func (client *Client) GetCurrentRepository() (*github.Repository, error) {
	if client.current_repository == nil {
		return nil, errors.New("Client has no current repository")
	}

	return client.current_repository, nil
}

func (client *Client) CreateRepository(name *string, description *string, private *bool) (err error) {
	client.current_repository, _, err = client.Repositories.Create(client.ctx, "", &github.Repository{
		Name:        name,
		Private:     private,
		Description: description,
	})

	return
}

func (client *Client) CreateDeployKey(name *string, key *string) error {
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

func (client *Client) CreatePushHook(name *string, url *string, devMode bool) (int64, string, error) {
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
		Config: map[string]interface{}{
			"content_type": "json",
			"secret":       secret,
			"insecure_ssl": 0,
			"url":          url,
		},
		Active: github.Bool(true),
	})

	if err != nil {
		return 0, "", err
	}

	return *(hk.ID), secret, err
}

func (client *Client) ListMyRepos() map[string]interface{} {
	repos := make(map[string]interface{})
	for i := 1; ; i++ {
		rlo := github.RepositoryListOptions{ListOptions: github.ListOptions{Page: i, PerPage: 100}, Sort: "created"} //Visibility: "all", Type: "all"}

		_repos, _, err := client.Repositories.List(client.ctx, "", &rlo)
		// TODO: Simplify this logic
		if err == nil && len(_repos) > 0 {
			for _, v := range _repos {
				repos[fmt.Sprintf("%d", *(v.ID))] = map[string]string{
					"name": *(v.FullName),
					"url":  *(v.URL),
				}
			}
		} else {
			break
		}
	}
	return repos
}

func (client *Client) ShortRepositoryInfo(id string) map[string]interface{} {
	repoInfo := make(map[string]interface{})

	_id, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		repoInfo["error"] = "Incorrect repository ID"
		return repoInfo
	}

	_repoInfo, _, err := client.Repositories.GetByID(client.ctx, _id)
	if err != nil {
		repoInfo["error"] = fmt.Sprintf("Error %s", err)
		return repoInfo
	}

	repoInfo["name"] = *(_repoInfo.Name)
	repoInfo["fullname"] = *(_repoInfo.FullName)
	repoInfo["url"] = *(_repoInfo.URL)
	repoInfo["id"] = id

	return repoInfo
}
