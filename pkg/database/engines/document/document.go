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
)

// Store is the same four-method door the kv engine keeps.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// Name is the engine name the vocabulary uses for tau's own.
const Name = "distributed"

var (
	ErrNoStore     = errors.New("document: no store to open onto")
	ErrNotFound    = errors.New("document: no such document")
	ErrNoID        = errors.New("document: no id")
	ErrNotAnObject = errors.New("document: a document is an object")
	ErrSeparator   = errors.New("document: a project, environment or database name may not contain \"/\"")
)

// Engine opens document databases over one store.
type Engine struct{ store Store }

func New(store Store) (*Engine, error) {
	if store == nil {
		return nil, ErrNoStore
	}
	return &Engine{store: store}, nil
}

func (e *Engine) Name() string           { return Name }
func (e *Engine) Types() []database.Type { return []database.Type{database.Document} }

// Open answers a connection scoped to one database of one deployment. An
// unaddressable spec is a connection that fails rather than an open that does —
// the registry probes with one, proving the surface and not an address.
func (e *Engine) Open(_ context.Context, spec database.Spec) (database.Conn, error) {
	if spec.Type != database.Document {
		return nil, fmt.Errorf("document: this engine backs %s and not %s", database.Document, spec.Type)
	}

	prefix, err := prefixOf(spec)
	return &conn{store: e.store, prefix: prefix, err: err}, nil
}

func prefixOf(spec database.Spec) (string, error) {
	for _, segment := range []string{spec.Project, spec.Environment, spec.Name} {
		if segment == "" {
			return "", errors.New("document: a database with no project, environment or name addresses nothing")
		}
		if strings.Contains(segment, "/") {
			return "", fmt.Errorf("%w: %s/%s/%s", ErrSeparator, spec.Project, spec.Environment, spec.Name)
		}
	}
	return strings.Join([]string{spec.Project, spec.Environment, spec.Name}, "/") + "/", nil
}

type conn struct {
	store  Store
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

	raw, found, err := c.store.Get(ctx, c.prefix+id)
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
	return c.store.Put(ctx, c.prefix+id, raw)
}

func (c *conn) Delete(ctx context.Context, id string) error {
	if c.err != nil {
		return c.err
	}
	if id == "" {
		return ErrNoID
	}
	return c.store.Delete(ctx, c.prefix+id)
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
