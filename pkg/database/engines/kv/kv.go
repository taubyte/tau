// Package kv backs the `kv` data model with a replicated key-value store.
//
// # It is what makes a database work with nothing to bring
//
// Every other engine in this tree reaches a server somebody else runs: a
// Postgres, a MySQL, a Mongo. This one is tau's own — `engine: distributed` —
// so a project that declares a `kv` database gets one from the swarm it is
// already on, with no connection string, no credential and nothing to
// provision. That is the whole of the difference between "declare a database"
// and "go and get a database first".
//
// # A system of record, which a cache is not
//
// The vocabulary is explicit that these are different kinds for a reason: a
// cache may lose an entry and a database may not. So there is NO ttl here and
// NO eviction — the two things pkg/cache exists to do — and a full store
// refuses a write rather than dropping somebody's record to make room.
//
// # What it does not promise, said here rather than discovered
//
// CONCURRENT WRITES TO ONE KEY MERGE LAST-WRITER-WINS, and the loser is gone
// with no error. That is the CRDT underneath and it is what `consensus: crdt`
// asks for; the vocabulary says the same thing where it declares the type. The
// rule that falls out is AGENTS.md's: never a key two writers can write
// differently. `consensus: raft` orders writes to a key and is NOT served here
// — it needs the writes routed through a raft leader, which is a different
// store.
//
// THERE IS NO COMPARE-AND-SWAP AND NO TRANSACTION, and no kind in this
// vocabulary offers one.
package kv

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/database"
)

// Store is where entries live: the same four-method door services/secrets and
// pkg/cache keep over a kvdb, and for the same reason — the rules above are
// testable against a map, and which node's write another sees is the store's
// business rather than this package's.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// Name is the engine name the vocabulary uses for tau's own.
const Name = "distributed"

var (
	ErrNoStore  = errors.New("kv: no store to open onto")
	ErrNotFound = errors.New("kv: no such key")
	ErrNoKey    = errors.New("kv: no key")
	ErrFull     = errors.New("kv: this database is at its declared size")
	ErrNoWatch  = errors.New("kv: watch is not served yet")
)

// Engine opens kv databases over one store.
//
// ONE STORE FOR EVERY DATABASE ON A NODE, separated by a key prefix — the same
// arrangement node/cache.go keeps, and for the same reason: a store per
// resource would be a CRDT, a broadcaster and a pubsub topic each.
type Engine struct{ store Store }

// New builds the engine. It is handed a store rather than building one: what a
// replicated store is made of belongs to whoever composed the node.
func New(store Store) (*Engine, error) {
	if store == nil {
		return nil, ErrNoStore
	}
	return &Engine{store: store}, nil
}

func (e *Engine) Name() string           { return Name }
func (e *Engine) Types() []database.Type { return []database.Type{database.KV} }

// Open answers a connection scoped to one database of one deployment.
//
// NOTHING IS DIALLED AND NOTHING IS ALLOCATED. A kv database is a prefix over a
// store that is already open, so opening one is composing a string.
//
// AN UNADDRESSABLE SPEC IS A CONNECTION THAT FAILS, NOT AN OPEN THAT DOES.
// pkg/database's registration probe opens one connection per type with no
// project and no environment — it is proving the SURFACE, not an address — and
// an engine that refused it could not be registered at all. So the address is
// checked here and reported where it is used.
func (e *Engine) Open(_ context.Context, spec database.Spec) (database.Conn, error) {
	if spec.Type != database.KV {
		return nil, fmt.Errorf("kv: this engine backs %s and not %s", database.KV, spec.Type)
	}

	prefix, err := spec.Prefix()
	return &conn{store: e.store, prefix: prefix, err: err}, nil
}

// prefixOf is where this database's entries live.
//
// THE SEPARATOR CHECK IS THE ISOLATION, exactly as it is for a secret: without
// it a database literally named "../production/ledger" in staging would be a
// readable path into production's.

// conn is one database.
type conn struct {
	store  Store
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

	value, found, err := c.store.Get(ctx, c.prefix+key)
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
	return c.store.Put(ctx, c.prefix+key, value)
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
	return c.store.Delete(ctx, c.prefix+key)
}

// List names the keys under a prefix, sorted, WITHOUT this database's own
// prefix on them — a caller asked about its own keys and should get its own
// keys back.
func (c *conn) List(ctx context.Context, prefix string) ([]string, error) {
	if c.err != nil {
		return nil, c.err
	}

	keys, err := c.store.List(ctx, c.prefix+prefix)
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

// Watch is declared by the type and not served.
//
// IT IS AN ERROR AND NOT A SILENT EMPTY CHANNEL. A caller handed a channel that
// never delivers waits forever for a change it would have been told about; one
// handed an error knows the platform does not do this yet. What it needs is a
// change feed out of the store underneath, which the CRDT does not expose.
func (c *conn) Watch(context.Context, string) (<-chan string, error) {
	return nil, ErrNoWatch
}

// Close releases nothing, because Open allocated nothing: the store belongs to
// whoever composed the engine and is shared by every database on the node.
func (c *conn) Close() error { return nil }
