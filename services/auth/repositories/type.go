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
}

type RepositoryCommon struct {
	KV       kvdb.KVDB
	Provider string
	Project  string
}

type GithubRepository struct {
	RepositoryCommon
	Id  int
	Key string
}
