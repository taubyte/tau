package repositories

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-interfaces/kvdb"
	"github.com/taubyte/tau/protocols/auth/hooks"
)

var (
	GitProviders = []string{"github"}
	logger       = log.Logger("tau.auth.service.api.repositories")
)

func (r *GithubRepository) Serialize() Data {
	return Data{
		"id":       r.Id,
		"provider": r.Provider,
		"project":  r.Project,
		"key":      r.Key,
	}
}

func (r *GithubRepository) Delete(ctx context.Context) (err error) {
	var lError error

	// let's clear the hooks first
	for _, h := range r.Hooks(ctx) {
		err = h.Delete(ctx)
		if err != nil {
			lError = err
		}
	}

	// TODO: This can be a common method
	repo_key := fmt.Sprintf("/repositories/github/%d/key", r.Id)
	err = r.KV.Delete(ctx, repo_key)
	if err != nil {
		lError = err
	}

	return lError
}

func (r *GithubRepository) Register(ctx context.Context) (err error) {
	repo_key := fmt.Sprintf("/repositories/github/%d/key", r.Id)
	err = r.KV.Put(ctx, repo_key, []byte(r.Key))
	if err != nil {
		r.Delete(ctx)
		return err
	}

	return nil
}

func (r *GithubRepository) Hooks(ctx context.Context) []hooks.Hook {
	keys, err := r.KV.List(ctx, fmt.Sprintf("/repositories/github/%d/hooks/", r.Id))
	if err != nil {
		return nil
	}

	hks := make([]hooks.Hook, 0)
	re := regexp.MustCompile("/hooks/([^/]+)$")
	for _, k := range keys {
		m := re.FindStringSubmatch(k)
		logger.Debugf("repo.Hooks match:%s", m)
		if len(m) > 1 {
			hook_id := m[1]
			h, err := hooks.Fetch(ctx, r.KV, hook_id)
			if err == nil {
				hks = append(hks, h)
			} else {
				logger.Error(err)
			}
		}
	}
	return hks
}

func Exist(ctx context.Context, kv kvdb.KVDB, id string) bool {
	for _, p := range GitProviders {
		ret, err := kv.Get(ctx, fmt.Sprintf("/repositories/%s/%s/key", p, id))
		if err == nil && ret != nil {
			return true
		}
	}
	return false
}

func ExistOn(ctx context.Context, kv kvdb.KVDB, provider, id string) bool {
	ret, err := kv.Get(ctx, fmt.Sprintf("/repositories/%s/%s/key", provider, id))
	return err == nil && ret != nil
}

func Provider(ctx context.Context, kv kvdb.KVDB, id string) (string, error) {
	var err error
	var ret []byte
	for _, p := range GitProviders {
		ret, err = kv.Get(ctx, fmt.Sprintf("/repositories/%s/%s/key", p, id))
		if err == nil && ret != nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("Repository with ID = `%s` does not exist! error: %w", id, err)
}

func fetchGithub(ctx context.Context, kv kvdb.KVDB, id int) (Repository, error) {
	repo_key := fmt.Sprintf("/repositories/github/%d", id)

	key, err := kv.Get(ctx, repo_key+"/key")
	if err != nil {
		return nil, err
	}

	// Ignoring the error here because not all repositories
	// have a project
	projectId, _ := kv.Get(ctx, repo_key+"/project")
	return New(kv, Data{
		"id":       id,
		"provider": "github",
		"project":  string(projectId),
		"key":      string(key),
	})

}

// TODO: ask for provider
// id: provided as string even if it's an int
func Fetch(ctx context.Context, kv kvdb.KVDB, id string) (Repository, error) {
	// figure out the provider
	provider, err := Provider(ctx, kv, id)
	if err != nil {
		return nil, err
	}

	switch provider {
	case "github":
		_id, err := strconv.Atoi(id)
		if err != nil {
			return nil, errors.New("Github repository id must be an int. Parsing returned: " + err.Error())
		}
		repo, err := fetchGithub(ctx, kv, _id)
		if err != nil {
			return nil, errors.New("Failed fetching github with: " + err.Error())
		}
		return repo, nil
	}
	return nil, errors.New("unknown/unsupported git provider " + provider)
}
