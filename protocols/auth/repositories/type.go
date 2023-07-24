package repositories

import (
	"context"

	"github.com/taubyte/go-interfaces/kvdb"
	"github.com/taubyte/odo/protocols/auth/hooks"
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
