// Package document backs the `document` data model with a replicated store.
//
// # Another database with nothing to provision
//
// This is the kv engine's argument again: `engine: distributed` is tau's own,
// so a project that declares a document database gets one from the swarm it is
// already on. What it adds over kv is the second verb the type declares —
// finding a document by its FIELDS rather than by its id.
//
// # A query is a scan, and that is a ceiling rather than a defect
//
// There is no index. `Query` walks the collection and matches, so the cost is
// the collection's size and not the answer's. That is fine for what a
// system-of-record document store usually holds — thousands of orders, not
// billions of events — and it is stated here because the alternative is
// somebody discovering it at a million documents.
//
// WHAT WOULD LIFT IT is a secondary index, which is a second key space kept in
// step with the first: `field/value/<id>` beside `<id>`. That is a real design
// — what to index is a decision the vocabulary does not currently let an author
// state — so it is named rather than half-built.
//
// # What a filter means
//
// EQUALITY ON EVERY FIELD NAMED, and nothing else. `{status: "paid", region:
// "eu"}` finds documents that have both. There are no operators, no ranges and
// no nesting into arrays, because the vocabulary declares one verb called
// `query` and says nothing about a query LANGUAGE — inventing one here would be
// putting a grammar in a map that no config can describe and no other engine
// would implement the same way.
//
// A DOTTED NAME REACHES INTO A NESTED OBJECT — `{"address.city": "algiers"}` —
// which is the one exception, and it is not an operator: it is how every
// document store spells a path, and without it a nested document could only be
// matched whole.
package document

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/database"

	"github.com/taubyte/tau/pkg/storekey"
)

// Store is the same four-method door the kv engine keeps.
// Name is the engine name the vocabulary uses for tau's own.
const Name = "distributed"

var (
	ErrNotFound    = errors.New("document: no such document")
	ErrNoID        = errors.New("document: no id")
	ErrNotAnObject = errors.New("document: a document is an object")
)

// Engine opens document databases, each over the store its provider hands out.
//
// A PROVIDER AND NOT A STORE: see database.Stores. One store for every database
// on a node is one pubsub topic carrying every database's rows, which makes
// placement a decoration and makes a local drop a fleet-wide delete.
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
func (e *Engine) Types() []database.Type { return []database.Type{database.Document} }

// Open answers a connection scoped to one database of one deployment. An
// unaddressable spec is a connection that fails rather than an open that does —
// the registry probes with one, proving the surface and not an address.
func (e *Engine) Open(ctx context.Context, spec database.Spec) (database.Conn, error) {
	if spec.Type != database.Document {
		return nil, fmt.Errorf("document: this engine backs %s and not %s", database.Document, spec.Type)
	}

	prefix, err := spec.Prefix()
	if err != nil {
		// THE REGISTRATION PROBE LANDS HERE. pkg/database opens one connection
		// per type with no project and no environment to prove the SURFACE, not
		// an address — so asking the provider for that database's store would
		// refuse to register an engine that is perfectly good. The address is
		// reported where it is used instead.
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
	err    error
}

var _ database.DocumentConn = (*conn)(nil)

func (c *conn) Get(ctx context.Context, id string) (map[string]any, error) {
	if c.err != nil {
		return nil, c.err
	}
	if id == "" {
		return nil, ErrNoID
	}

	at, err := c.key(id)
	if err != nil {
		return nil, err
	}
	raw, found, err := c.store.Get(ctx, at)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return decode(raw)
}

// Put writes a document. It is an upsert, like every other Put in this tree.
//
// JSON AND NOT A GO ENCODING. What is stored has to be readable by something
// that is not this process — another node, a different build, a person looking
// at the store — and gob would make the rows a Go program's private business.
func (c *conn) Put(ctx context.Context, id string, doc map[string]any) error {
	if c.err != nil {
		return c.err
	}
	if id == "" {
		return ErrNoID
	}
	if doc == nil {
		return ErrNotAnObject
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("document: %s is not encodable: %w", id, err)
	}
	at, err := c.key(id)
	if err != nil {
		return err
	}
	return c.store.Put(ctx, at, raw)
}

func (c *conn) Delete(ctx context.Context, id string) error {
	if c.err != nil {
		return c.err
	}
	if id == "" {
		return ErrNoID
	}
	at, err := c.key(id)
	if err != nil {
		return err
	}
	return c.store.Delete(ctx, at)
}

// Query walks the collection and returns what matches, ordered by id so two
// runs of one query answer the same way.
//
// AN EMPTY FILTER IS EVERYTHING, which is what a document store's own APIs mean
// by it and what makes "list this collection" expressible without a second verb.
func (c *conn) Query(ctx context.Context, filter map[string]any) ([]map[string]any, error) {
	if c.err != nil {
		return nil, c.err
	}

	keys, err := c.store.List(ctx, c.prefix)
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)

	out := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		raw, found, err := c.store.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		if !found {
			// Taken by a concurrent delete between the list and the read. Not
			// an error: it is not in the collection any more.
			continue
		}

		doc, err := decode(raw)
		if err != nil {
			// A ROW THIS ENGINE DID NOT WRITE IS SKIPPED rather than failing
			// the query — one unreadable document must not make the collection
			// unqueryable.
			continue
		}
		if matches(doc, filter) {
			out = append(out, doc)
		}
	}
	return out, nil
}

func (c *conn) Close() error { return nil }

func decode(raw []byte) (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotAnObject, err)
	}
	return doc, nil
}

// matches is equality on every field the filter names. See the package doc for
// why there is no operator.
func matches(doc, filter map[string]any) bool {
	for path, want := range filter {
		got, found := at(doc, path)
		if !found || !equal(got, want) {
			return false
		}
	}
	return true
}

// at reads a field, descending on dots.
func at(doc map[string]any, path string) (any, bool) {
	var current any = doc
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// equal compares a stored value with a filter's.
//
// EVERY NUMBER THAT CAME OUT OF JSON IS A float64, and a filter written in Go
// carries whatever the caller had — an int, an int64, a float. Comparing them
// with == answers false for 1 against 1.0, which is a query that silently finds
// nothing. So numbers are compared as numbers.
func equal(got, want any) bool {
	if a, ok := number(got); ok {
		if b, ok := number(want); ok {
			return a == b
		}
		return false
	}
	return got == want
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
}

// key is this store's addressing of a key a GUEST supplied: the prefix it
// belongs to, joined, once it is known the join stays inside that prefix.
//
// IT IS ONE METHOD AND NOT A CHECK AT EACH CALL, so a method added later gets
// the rule by addressing an entry at all rather than by its author remembering
// to. See pkg/storekey for what the rule is, and what not having it cost.
func (c *conn) key(k string) (string, error) {
	if err := storekey.Check(c.prefix, k); err != nil {
		return "", err
	}
	return c.prefix + k, nil
}
