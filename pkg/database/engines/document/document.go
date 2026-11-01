// Package document backs the `document` data model with a replicated store: tau's
// own, nothing to provision. Beyond kv it finds documents by fields.
//
// There is no index: Query scans the collection, so cost is the collection's size,
// fine for thousands of documents. A secondary index (`field/value/<id>`) would lift
// it, but what to index is something the vocabulary cannot state yet.
//
// A filter is equality on every field named; no operators, ranges or array matching,
// since the vocabulary declares `query` but no query language. A dotted name reaches
// into a nested object (`{"address.city": "algiers"}`).
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

// Name is the engine name the vocabulary uses for tau's own.
const Name = "distributed"

var (
	ErrNotFound    = errors.New("document: no such document")
	ErrNoID        = errors.New("document: no id")
	ErrNotAnObject = errors.New("document: a document is an object")
)

// Engine opens document databases, each over the store its provider hands out (see
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
func (e *Engine) Types() []database.Type { return []database.Type{database.Document} }

// Open answers a connection scoped to one database. An unaddressable spec yields a
// connection that fails rather than a failed open, since the registry probes with
// one.
func (e *Engine) Open(ctx context.Context, spec database.Spec) (database.Conn, error) {
	if spec.Type != database.Document {
		return nil, fmt.Errorf("document: this engine backs %s and not %s", database.Document, spec.Type)
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

// Put writes a document (an upsert) as JSON, so other nodes, builds and people can
// read it.
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

// Query walks the collection and returns matches ordered by id. An empty filter is
// everything.
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

// equal compares a stored value with a filter's. JSON numbers are float64 and a Go
// filter carries any numeric type, so numbers are compared as numbers.
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

// key addresses a guest-supplied key under this database's prefix, refusing one that
// escapes it (pkg/storekey). One method, so every operation gets the rule.
func (c *conn) key(k string) (string, error) {
	if err := storekey.Check(c.prefix, k); err != nil {
		return "", err
	}
	return c.prefix + k, nil
}
