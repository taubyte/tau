// Package database is the seam every database engine plugs into. A database
// declares a type (data model) and, within it, an engine:
//
//	kv        distributed
//	document  distributed, mongo
//	sql       distributed, postgres, mysql
//	vector    distributed
//
// `distributed` is tau's own, replicated across the swarm; the rest are external
// servers reached through adapters over their usual drivers.
//
// The four types do different things, so a Conn carries only Close and a consumer
// asks for the surface it needs. Register proves an engine implements each type it
// claims by opening a probe connection, so a mismatch fails at startup rather than on
// a first query. This package depends on no driver.
package database

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
)

// Type is a data model, spelled as the vocabulary spells it.
type Type string

// The types the vocabulary declares; a test asserts this set matches the schema.
const (
	KV       Type = "kv"
	Document Type = "document"
	SQL      Type = "sql"
	Vector   Type = "vector"
)

// Tau is the engine name for tau's own, replicated across the swarm. External
// engines carry the name of the server they speak to.
const Tau = "distributed"

// How concurrent writes to one key resolve, in the schema's own words.
const (
	// CRDT merges them. No write is lost on the wire, but two writes to one key
	// are resolved into one and the loser is gone with no error to anybody.
	CRDT = "crdt"

	// Raft orders them: one holder at a time, so a write costs a round trip and
	// nothing is silently discarded. It is what an author writes when entries
	// must not resolve by merge -- a ledger cannot be kept the other way.
	Raft = "raft"
)

var (
	ErrNoEngine     = errors.New("database: no engine registered for this type")
	ErrNoName       = errors.New("database: an engine with no name")
	ErrNoTypes      = errors.New("database: an engine that backs no type")
	ErrDuplicate    = errors.New("database: that engine is registered for that type already")
	ErrWrongSurface = errors.New("database: the engine does not implement its type's operations")
	ErrUnknownType  = errors.New("database: not a data model this vocabulary declares")

	// ErrSeparator is an address segment carrying the separator between
	// segments. It is the isolation: without it a database literally named
	// "../production/ledger" in staging would be a readable path into
	// production's, exactly as it would be for a secret.
	ErrSeparator = errors.New(`database: a project, environment or database name may not contain "/"`)

	// ErrUnaddressable is a spec with a hole where an address segment belongs.
	ErrUnaddressable = errors.New("database: a spec with no project, environment, model or name addresses nothing")

	// ErrNoStore is an engine with nowhere to keep entries.
	ErrNoStore = errors.New("database: no store to open onto")
)

type Spec struct {
	Type Type

	// Engine is which engine backs it — Tau, or the name of an external server.
	Engine string

	// Project, Environment and Name address the resource. A database is
	// per-environment like everything else the compiled project carries.
	Project     string
	Environment string
	Name        string

	// Consensus is how concurrent writes to one key resolve. Empty is CRDT. Beside Engine
	// rather than in Options: it says what the connection is, and a store is chosen by it
	// before any per-model field is read.
	Consensus string

	// Size is the ceiling the author declared, in bytes, for tau's own engine.
	// Zero is no ceiling, which only a connector to somebody else's server has.
	Size int64

	// Address is where an external server is, empty for Tau's own engine.
	Address string

	// Database is the name within that server, for engines that have such a
	// notion. Empty for those that do not.
	Database string

	// Credentials is what opens it. It is a VALUE here and not a reference,
	// because resolving a secret reference is the caller's job and this package
	// must never become a thing that reads secrets.
	Credentials Credentials

	// Options are what only the resource's own type understands (a vector's width and
	// metric), keyed by the vocabulary's attribute names.
	Options map[string]string
}

type Credentials struct {
	Username string
	Password string

	// TLS is whether the wire is protected. It has no default here: an engine
	// that silently connected in the clear because a field was unset would be a
	// security decision made by omission.
	TLS bool
}

// Conn is an open database. It carries only Close, because what else it can do
// depends on its type — see the package doc.
type Conn interface {
	io.Closer
}

type Engine interface {
	Name() string

	Types() []Type

	// Open connects. The returned Conn must implement the operation surface of
	// spec.Type; Register proves that once rather than trusting it.
	Open(ctx context.Context, spec Spec) (Conn, error)
}

// The per-type operation surfaces mirror the verbs the vocabulary declares; a method
// no `Ops()` entry names would be a capability config cannot grant.

// KVConn is `kv`: get, list, watch, put, delete.
type KVConn interface {
	Conn
	Get(ctx context.Context, key string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]string, error)
	Watch(ctx context.Context, prefix string) (<-chan string, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
}

// SQLConn is `sql`: query, exec.
type SQLConn interface {
	Conn
	Query(ctx context.Context, statement string, args ...any) (Rows, error)
	Exec(ctx context.Context, statement string, args ...any) (Outcome, error)
}

// Outcome is what a write changed, in the shape SQL clients report it. Both fields
// are 0 when the driver did not say, which is not an error: failing a statement that
// ran because its result cannot be counted would report success as failure.
type Outcome struct {
	Affected     int64
	LastInsertID int64
}

// Rows is a result set. It is deliberately not database/sql.Rows: this seam has
// no driver dependency, and an adapter over one is a small wrapper.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Columns() ([]string, error)
	Err() error
	io.Closer
}

// DocumentConn is `document`: get, query, put, delete.
type DocumentConn interface {
	Conn
	Get(ctx context.Context, id string) (map[string]any, error)
	Query(ctx context.Context, filter map[string]any) ([]map[string]any, error)
	Put(ctx context.Context, id string, doc map[string]any) error
	Delete(ctx context.Context, id string) error
}

// VectorConn is `vector`: search, upsert, delete.
type VectorConn interface {
	Conn
	Search(ctx context.Context, query []float32, k int) ([]Match, error)
	Upsert(ctx context.Context, id string, embedding []float32, meta map[string]any) error
	Delete(ctx context.Context, id string) error
}

// Match is one nearest-neighbour result.
type Match struct {
	ID    string
	Score float32
	Meta  map[string]any
}

// implements reports whether c satisfies the operation surface of t.
func implements(t Type, c Conn) bool {
	switch t {
	case KV:
		_, ok := c.(KVConn)
		return ok
	case SQL:
		_, ok := c.(SQLConn)
		return ok
	case Document:
		_, ok := c.(DocumentConn)
		return ok
	case Vector:
		_, ok := c.(VectorConn)
		return ok
	}
	return false
}

func Types() []Type { return []Type{Document, KV, SQL, Vector} }

func known(t Type) bool { return slices.Contains(Types(), t) }

// Registry is which engine backs which type.
type Registry struct {
	mu sync.RWMutex

	engines map[Type]map[string]Engine
}

func NewRegistry() *Registry {
	return &Registry{engines: map[Type]map[string]Engine{}}
}

// Register adds an engine, proving it can do what it claims by opening a probe
// connection per type: Open returns the interface, so the surface can only be checked
// on a real Conn.
func (r *Registry) Register(ctx context.Context, e Engine) error {
	if e == nil {
		return errors.New("database: a nil engine")
	}
	name := e.Name()
	if name == "" {
		return ErrNoName
	}
	types := e.Types()
	if len(types) == 0 {
		return fmt.Errorf("%w: %s", ErrNoTypes, name)
	}

	for _, t := range types {
		if !known(t) {
			return fmt.Errorf("%w: %s claims %q", ErrUnknownType, name, t)
		}
	}

	r.mu.Lock()
	for _, t := range types {
		if _, dup := r.engines[t][name]; dup {
			r.mu.Unlock()
			return fmt.Errorf("%w: %s/%s", ErrDuplicate, t, name)
		}
	}
	r.mu.Unlock()

	// The probe is outside the lock: opening may touch the network.
	for _, t := range types {
		conn, err := e.Open(ctx, probeSpec(t, name))
		if err != nil {
			return fmt.Errorf("database: %s cannot open a %s: %w", name, t, err)
		}
		ok := implements(t, conn)
		conn.Close()
		if !ok {
			return fmt.Errorf("%w: %s claims %s and answers none of its operations", ErrWrongSurface, name, t)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range types {
		if r.engines[t] == nil {
			r.engines[t] = map[string]Engine{}
		}
		if _, dup := r.engines[t][name]; dup {
			return fmt.Errorf("%w: %s/%s", ErrDuplicate, t, name)
		}
		r.engines[t][name] = e
	}
	return nil
}

// probeSpec is a well-formed spec naming nowhere: an address in the reserved
// `.invalid` TLD (RFC 2606), so external engines can register. An engine must
// therefore not connect in Open, which database/sql and good clients do lazily anyway.
func probeSpec(t Type, engine string) Spec {
	return Spec{
		Type:     t,
		Engine:   engine,
		Name:     "probe",
		Address:  "probe.invalid:0",
		Database: "probe",
	}
}

func (r *Registry) Open(ctx context.Context, spec Spec) (Conn, error) {
	if !known(spec.Type) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownType, spec.Type)
	}

	r.mu.RLock()
	e, ok := r.engines[spec.Type][spec.Engine]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrNoEngine, spec.Type, spec.Engine)
	}

	conn, err := e.Open(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("database: opening %s/%s: %w", spec.Type, spec.Engine, err)
	}
	return conn, nil
}

func (r *Registry) EnginesFor(t Type) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.engines[t]))
	for name := range r.engines[t] {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// String is what an operator sees when asking what this node can back.
func (r *Registry) String() string {
	var b strings.Builder
	for _, t := range Types() {
		names := r.EnginesFor(t)
		if len(names) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", t, strings.Join(names, ", "))
	}
	return b.String()
}

// Prefix is where a resident engine keeps this database's entries, the one addressing
// rule all engines share. The model is in the path: an id is stable across edits, so
// changing `kv:` to `vector:` must not hand the vector store the kv rows. An id
// deleted and redeclared as the same model shares a path until the sweep runs.
func (s Spec) Prefix() (string, error) {
	segments := []string{s.Project, s.Environment, string(s.Type), s.Name}
	for _, segment := range segments {
		if segment == "" {
			return "", ErrUnaddressable
		}
		if strings.Contains(segment, "/") {
			return "", fmt.Errorf("%w: %s", ErrSeparator, strings.Join(segments, "/"))
		}
	}
	return strings.Join(segments, "/") + "/", nil
}
