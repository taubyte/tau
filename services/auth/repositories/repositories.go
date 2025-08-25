package repositories

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/services/auth/hooks"
)

var (
	GitProviders = []string{"github"}
	logger       = log.Logger("tau.auth.service.api.repositories")
)

func (r *githubRepository) Serialize() Data {
	return Data{
		"id":       r.id,
		"provider": r.provider,
		"project":  r.project,
		"key":      r.key,
	}
}

func (r *githubRepository) Delete(ctx context.Context) (err error) {
	for _, h := range r.Hooks(ctx) {
		err = h.Delete(ctx)
		if err != nil {
			logger.Errorf("Failed to delete hook %s: %v", h.ID(), err)
		}
	}

	batch, err := r.kv.Batch(ctx)
	if err != nil {
		return fmt.Errorf("failed to create batch: %w", err)
	}

	repo_key := fmt.Sprintf("/repositories/github/%d/key", r.id)
	err = batch.Delete(repo_key)
	if err != nil {
		return fmt.Errorf("failed to batch delete repository key: %w", err)
	}

	err = batch.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit repository deletion batch: %w", err)
	}

	return nil
}

func (r *githubRepository) Register(ctx context.Context) (err error) {
	batch, err := r.kv.Batch(ctx)
	if err != nil {
		return fmt.Errorf("failed to create batch: %w", err)
	}

	repo_key := fmt.Sprintf("/repositories/github/%d/key", r.id)
	err = batch.Put(repo_key, []byte(r.key))
	if err != nil {
		return fmt.Errorf("failed to batch repository key: %w", err)
	}

	err = batch.Commit()
	if err != nil {
		r.Delete(ctx)
		return fmt.Errorf("failed to commit repository registration batch: %w", err)
	}

	return nil
}

func (r *githubRepository) Hooks(ctx context.Context) []hooks.Hook {
	keys, err := r.kv.List(ctx, fmt.Sprintf("/repositories/github/%d/hooks/", r.id))
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
			h, err := hooks.Fetch(ctx, r.kv, hook_id)
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

	projectId, _ := kv.Get(ctx, repo_key+"/project")
	return New(kv, Data{
		"id":       id,
		"provider": "github",
		"project":  string(projectId),
		"key":      string(key),
	})

}

func Fetch(ctx context.Context, kv kvdb.KVDB, id string) (Repository, error) {
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
