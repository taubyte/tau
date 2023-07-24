package repositories

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-interfaces/kvdb"
	"github.com/taubyte/odo/protocols/auth/hooks"
)

var (
	GitProviders = []string{"github"}
	logger       = log.Logger("auth.service.api.repositories")
)

func (r *GithubRepository) Serialize() Data {
	return Data{
		"id":       r.Id,
		"provider": r.Provider,
		//"name":     r.Name,
		"project": r.Project,
		"key":     r.Key,
		//"url":      r.Url,
	}
}

func (r *GithubRepository) Delete(ctx context.Context) error {
	var (
		lerror error
		err    error
	)

	// let's clear the hooks first
	for _, h := range r.Hooks(ctx) {
		err = h.Delete(ctx)
		if err != nil {
			lerror = err
		}
	}

	repo_key := fmt.Sprintf("/repositories/github/%d", r.Id)

	// err = r.KV.Delete(repo_key + "/name")
	// if err != nil {
	// 	lerror = err
	// }

	// err = r.KV.Delete(repo_key + "/project")
	// if err != nil {
	// 	lerror = err
	// }

	err = r.KV.Delete(ctx, repo_key+"/key")
	if err != nil {
		lerror = err
	}

	// err = r.KV.Delete(repo_key + "/url")
	// if err != nil {
	// 	lerror = err
	// }

	return lerror
}

func (r *GithubRepository) Register(ctx context.Context) error {
	var err error

	repo_key := fmt.Sprintf("/repositories/github/%d", r.Id)

	// err = r.KV.Put(repo_key+"/name", []byte(r.Name))
	// if err != nil {
	// 	r.Delete()
	// 	return err
	// }

	// err = r.KV.Put(repo_key+"/project", []byte(r.Project))
	// if err != nil {
	// 	r.Delete()
	// 	return err
	// }

	err = r.KV.Put(ctx, repo_key+"/key", []byte(r.Key))
	if err != nil {
		r.Delete(ctx)
		return err
	}

	// err = r.KV.Put(repo_key+"/url", []byte(r.Url))
	// if err != nil {
	// 	r.Delete()
	// 	return err
	// }

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
		logger.Errorf("repo.Hooks match:%s", m)
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

	// name, err := kv.Get(repo_key + "/name")
	// if err != nil {
	// 	return nil, err
	// }

	projectId, _ := kv.Get(ctx, repo_key+"/project")
	// Ignoring the error here because not all repositories
	// have a project

	key, err := kv.Get(ctx, repo_key+"/key")
	if err != nil {
		return nil, err
	}

	// url, err := kv.Get(repo_key + "/url")
	// if err != nil {
	// 	return nil, err
	// }

	return New(kv, Data{
		"id": id,
		//"name":     string(name),
		"provider": "github",
		"project":  string(projectId),
		"key":      string(key),
		//"url":      string(url),
	})

}

// FIX: ask for provider
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
