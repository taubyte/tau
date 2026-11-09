// Package projects is auth's project table: which configuration repository claimed a
// project id, which code repositories it attaches, who linked it, and the roles its
// owner granted. Auth serves it beside its users (ProjectService in auth.proto); a
// service reaches it through On, directly when auth runs beside it.
package projects

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	ds "github.com/ipfs/go-datastore"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/proto/codec"
)

// Store is the slice of a key-value store the table needs. Get separates "not
// there" from "could not look": a store that answered both with an error would turn
// an unreachable replica into a project nobody owns.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// Over is db as the table's store.
func Over(db kvdb.Instance) Store { return kvStore{db} }

type kvStore struct{ db kvdb.Instance }

func (s kvStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := s.db.Get(ctx, key)
	switch {
	case errors.Is(err, ds.ErrNotFound):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	return v, true, nil
}

func (s kvStore) Put(ctx context.Context, key string, value []byte) error {
	return s.db.Put(ctx, key, value)
}

func (s kvStore) List(ctx context.Context, prefix string) ([]string, error) {
	return s.db.List(ctx, prefix)
}

// Table is the project table over one store.
type Table struct {
	store Store

	// mu makes a link's checks and writes one step against another link on this
	// node. Across nodes nothing does; the resolution rule makes that safe.
	mu sync.Mutex

	// now timestamps a claim, which is what orders two claims on one id.
	now func() time.Time
}

// New is the table over store, its claims timestamped by now.
func New(store Store, now func() time.Time) *Table {
	return &Table{store: store, now: now}
}

const sep = "/"

var (
	ErrNoProject = errors.New("auth: a link names no project")
	ErrSeparator = errors.New("auth: a name may not contain the key separator")
)

func checkName(s string, empty error) error {
	if s == "" {
		return empty
	}
	if strings.Contains(s, sep) {
		return fmt.Errorf("%w: %q", ErrSeparator, s)
	}
	return nil
}

func (t *Table) put(ctx context.Context, key string, row codec.Record) error {
	b, err := row.MarshalVT()
	if err != nil {
		return fmt.Errorf("auth: encoding %s: %w", key, err)
	}
	if err := t.store.Put(ctx, key, b); err != nil {
		return fmt.Errorf("auth: writing %s: %w", key, err)
	}
	return nil
}

// read decodes the row at key into row. An empty row is a released one and reads as
// absent, and so does one that is present and unreadable: it cannot be ordered, so
// it cannot own anything, and refusing every read over it would let one bad write
// deny the owner.
func (t *Table) read(ctx context.Context, key string, row codec.Record) (bool, error) {
	b, found, err := t.store.Get(ctx, key)
	if err != nil {
		return false, fmt.Errorf("auth: reading %s: %w", key, err)
	}
	if !found || len(b) == 0 || codec.Decode(b, row) != nil {
		return false, nil
	}
	return true, nil
}
