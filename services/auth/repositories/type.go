package repositories

import (
	"context"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/services/auth/hooks"
)

type Data map[string]interface{}

type Repository interface {
	Register(ctx context.Context) error
	Delete(ctx context.Context) error
	Serialize() Data
	Hooks(ctx context.Context) []hooks.Hook
	ID() int
	Provider() string
}

type repositoryCommon struct {
	kv       kvdb.KVDB
	provider string
	project  string
}

type githubRepository struct {
	repositoryCommon
	id  int
	key string
}

func (r *githubRepository) ID() int {
	return r.id
}

func (r *githubRepository) Provider() string {
	return r.provider
}
