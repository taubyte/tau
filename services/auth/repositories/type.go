package repositories

import (
	"context"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/auth/hooks"
)

// Data is the repository's WIRE shape and nothing else. It used to be the
// constructor's argument too, and that is what made this store's contract
// invisible: `New(kv, Data{"provider":"github","id":123})` type-checks whatever
// the constructor expects, so changing what a repository is named by changed
// nothing the compiler would report. The constructor now takes a repokey.Key,
// and every caller that had to be found by grep is found by `go build`.
type Data map[string]interface{}

type Repository interface {
	Register(ctx context.Context) error
	Delete(ctx context.Context) error
	Serialize() Data
	Hooks(ctx context.Context) []hooks.Hook

	// ID is the canonical clone-URL key — `host/owner/name`. It is the whole
	// identity: there is no numeric id beside it and no provider field, because
	// the host in the key IS the provider.
	ID() string

	// Provider is the host that serves the repository. It is derived from the
	// key rather than stored, so the two cannot disagree.
	Provider() string
}

// repository is one repository, whatever serves it. There is no per-provider
// type any more: the provider was only ever a switch over which numeric id
// space a repository lived in, and a clone-URL key has one space.
type repository struct {
	kv        kvdb.KVDB
	key       repokey.Key
	project   string
	deployKey string
}

func (r *repository) ID() string {
	return r.key.String()
}

func (r *repository) Provider() string {
	return r.key.Host
}
