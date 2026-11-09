package cache

import (
	"context"
	"errors"
	"fmt"

	ds "github.com/ipfs/go-datastore"

	"github.com/taubyte/tau/core/kvdb"
)

// Over is db as a Store, separating "not there" from "could not look" the way a
// cache, an admission table or a volume's checkpoints need.
func Over(db kvdb.Instance) Store { return kv{db} }

type kv struct{ db kvdb.Instance }

func (s kv) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := s.db.Get(ctx, key)
	switch {
	case err == nil:
		return v, true, nil
	case errors.Is(err, ds.ErrNotFound):
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("cache: reading a row: %w", err)
	}
}

func (s kv) Put(ctx context.Context, key string, value []byte) error {
	return s.db.Put(ctx, key, value)
}

func (s kv) Delete(ctx context.Context, key string) error {
	if err := s.db.Delete(ctx, key); err != nil && !errors.Is(err, ds.ErrNotFound) {
		return err
	}
	return nil
}

func (s kv) List(ctx context.Context, prefix string) ([]string, error) {
	return s.db.List(ctx, prefix)
}
