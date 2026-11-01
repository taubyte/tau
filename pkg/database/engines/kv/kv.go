// Package kv backs the `kv` data model with a replicated key-value store: tau's own,
// so a declared kv database needs no connection string or credential.
//
// A system of record, not a cache: no ttl, no eviction, and a full store refuses a
// write. Concurrent writes to one key merge last-writer-wins (`consensus: crdt`);
// `consensus: raft` needs writes routed through a leader and is not served here. No
// compare-and-swap, no transactions.
package kv

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/database"

	"github.com/taubyte/tau/pkg/storekey"
)

// Name is the engine name the vocabulary uses for tau's own.
const Name = "distributed"

var (
	ErrNotFound = errors.New("kv: no such key")
	ErrNoKey    = errors.New("kv: no key")
	ErrFull     = errors.New("kv: this database is at its declared size")
	ErrNoWatch  = errors.New("kv: watch is not served yet")
)

// Engine opens kv databases, each over the store its provider hands out (see
// database.Stores).
type Engine struct{ stores database.Stores }

// New builds the engine. It is handed a provider rather than building one: what
// a replicated store is made of belongs to whoever composed the node.
func New(stores database.Stores) (*Engine, error) {
	if stores == nil {
		return nil, database.ErrNoStore
	}
	return &Engine{stores: stores}, nil
}

func (e *Engine) Name() string           { return Name }
func (e *Engine) Types() []database.Type { return []database.Type{database.KV} }

// Open answers a connection scoped to one database. Nothing is dialled: a database is
// a prefix over an open store. An unaddressable spec yields a connection that fails,
// since the registry's probe has no address.
func (e *Engine) Open(ctx context.Context, spec database.Spec) (database.Conn, error) {
	if spec.Type != database.KV {
		return nil, fmt.Errorf("kv: this engine backs %s and not %s", database.KV, spec.Type)
	}

	prefix, err := spec.Prefix()
	if err != nil {
		// The registration probe has no address; it is reported where used.
		return &conn{err: err}, nil
	}

	store, err := e.stores(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &conn{store: store, prefix: prefix}, nil
}

type conn struct {
	store  database.Store
	prefix string

	// err is why this connection addresses nothing, when it does. Every
	// operation answers it — see Open.
	err error
}

var _ database.KVConn = (*conn)(nil)

func (c *conn) Get(ctx context.Context, key string) ([]byte, error) {
	if c.err != nil {
		return nil, c.err
	}
	if key == "" {
		return nil, ErrNoKey
	}

	at, err := c.key(key)
	if err != nil {
		return nil, err
	}
	value, found, err := c.store.Get(ctx, at)
	if err != nil {
		return nil, err
	}
	if !found {
		// A MISS IS ErrNotFound AND NEVER AN EMPTY VALUE — the same rule the
		// secrets vault keeps, and for the same reason: a caller handed "" acts
		// on it and fails somewhere else, later.
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return value, nil
}

func (c *conn) Put(ctx context.Context, key string, value []byte) error {
	if c.err != nil {
		return c.err
	}
	if key == "" {
		return ErrNoKey
	}
	at, err := c.key(key)
	if err != nil {
		return err
	}
	return c.store.Put(ctx, at, value)
}

// Delete removes an entry. Deleting what is not there succeeds: the caller
// wanted it gone and it is gone.
func (c *conn) Delete(ctx context.Context, key string) error {
	if c.err != nil {
		return c.err
	}
	if key == "" {
		return ErrNoKey
	}
	at, err := c.key(key)
	if err != nil {
		return err
	}
	return c.store.Delete(ctx, at)
}

// List names the keys under a prefix, sorted, WITHOUT this database's own
// prefix on them — a caller asked about its own keys and should get its own
// keys back.
func (c *conn) List(ctx context.Context, prefix string) ([]string, error) {
	if c.err != nil {
		return nil, c.err
	}

	at, err := c.key(prefix)
	if err != nil {
		return nil, err
	}
	keys, err := c.store.List(ctx, at)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(keys))
	for _, full := range keys {
		_, key, ok := strings.Cut(full, c.prefix)
		if !ok || key == "" {
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out, nil
}

// Watch is declared by the type and not served: an error rather than a channel that
// never delivers. It needs a change feed the CRDT does not expose.
func (c *conn) Watch(context.Context, string) (<-chan string, error) {
	return nil, ErrNoWatch
}

// Close releases nothing, because Open allocated nothing: the store belongs to
// whoever composed the engine and is shared by every database on the node.
func (c *conn) Close() error { return nil }

// key addresses a guest-supplied key under this database's prefix, refusing one that
// escapes it (pkg/storekey). One method, so every operation gets the rule.
func (c *conn) key(k string) (string, error) {
	if err := storekey.Check(c.prefix, k); err != nil {
		return "", err
	}
	return c.prefix + k, nil
}
