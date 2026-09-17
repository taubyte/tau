// Package database is the seam every database engine plugs into.
//
// The vocabulary already decides the shape, and this package follows it rather
// than inventing one. A database declares a TYPE — the data model — and within
// that type an ENGINE:
//
//	kv        distributed
//	document  distributed, mongo
//	sql       distributed, postgres, mysql
//	vector    distributed
//
// `distributed` is tau's own, replicated across the swarm. The rest are external
// servers reached over the network, and for those the answer is an adapter over
// the driver everyone already uses rather than a reimplementation.
//
// # A type is an operation set, so there is no one Conn interface
//
// The four types do genuinely different things: kv gets and puts by key, sql
// queries and executes, document finds by a query over fields, vector searches
// by nearest neighbour. A single interface spanning all four would be four
// interfaces wearing a trenchcoat, and every implementation would have to answer
// methods it has no meaning for.
//
// So an engine returns a Conn that carries only Close, and a consumer asks for
// the surface it needs. That asking is checked ONCE, at registration, rather
// than on the first query — see Register.
//
// # What registration proves
//
// AN ENGINE THAT CLAIMS A TYPE MUST ACTUALLY IMPLEMENT THAT TYPE'S SURFACE, and
// it is proved by opening a probe connection at registration and type-asserting
// it. The alternative is discovering at the first query, in production, that the
// postgres adapter registered for `document` answers no document methods — a
// failure that reads as a bug in the caller.
//
// # What is not here
//
// NO DRIVER, NO IMPLEMENTATION. This package has no dependency on a database of
// any kind, which is what lets it be the thing both tau's own engine and a
// third-party adapter are written against. An engine lives beside its driver;
// the seam does not.
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

// The types the vocabulary declares. They are constants here so a consumer
// names one rather than a string literal, and a test asserts this set matches
// the schema.
const (
	KV       Type = "kv"
	Document Type = "document"
	SQL      Type = "sql"
	Vector   Type = "vector"
)

// Tau is the engine name for tau's own, replicated across the swarm. External
// engines carry the name of the server they speak to.
const Tau = "distributed"

var (
	ErrNoEngine     = errors.New("database: no engine registered for this type")
	ErrNoName       = errors.New("database: an engine with no name")
	ErrNoTypes      = errors.New("database: an engine that backs no type")
	ErrDuplicate    = errors.New("database: that engine is registered for that type already")
	ErrWrongSurface = errors.New("database: the engine does not implement its type's operations")
	ErrUnknownType  = errors.New("database: not a data model this vocabulary declares")
)

// Spec is which database to open.
type Spec struct {
	// Type is the data model.
	Type Type

	// Engine is which engine backs it — Tau, or the name of an external server.
	Engine string

	// Project, Environment and Name address the resource. A database is
	// per-environment like everything else the compiled project carries.
	Project     string
	Environment string
	Name        string

	// Address is where an external server is, empty for Tau's own engine.
	Address string

	// Database is the name within that server, for engines that have such a
	// notion. Empty for those that do not.
	Database string

	// Credentials is what opens it. It is a VALUE here and not a reference,
	// because resolving a secret reference is the caller's job and this package
	// must never become a thing that reads secrets.
	Credentials Credentials

	// Options are what the resource declared that only its own TYPE
	// understands — a vector's width and metric, and whatever the next type
	// brings.
	//
	// A MAP AND NOT A FIELD EACH, for the reason the Conn interface is not one
	// interface: the four types genuinely do different things, and a Spec with
	// `Dimensions` on it would carry a number that means nothing to three of
	// them. The keys are the vocabulary's own attribute names, so a reader
	// finds the attribute by searching the schema for the string.
	Options map[string]string
}

// Credentials open an external server.
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

// Engine opens connections for one or more types.
type Engine interface {
	// Name is the engine name the vocabulary uses.
	Name() string

	// Types are the data models this engine backs.
	Types() []Type

	// Open connects. The returned Conn must implement the operation surface of
	// spec.Type; Register proves that once rather than trusting it.
	Open(ctx context.Context, spec Spec) (Conn, error)
}

// ─────────────────────── the per-type operation surfaces ───────────────────────
//
// These mirror the verbs the vocabulary declares for each type, and nothing
// else. A method here that no `Ops()` entry names would be a capability config
// cannot grant, which is a hole in the authorization story rather than a
// convenience.

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

// Outcome is what a write changed, in the shape every SQL client already
// reports it: `database/sql.Result`, DBAPI's `rowcount` and `lastrowid`,
// node-postgres's `rowCount`.
//
// BOTH FIELDS ARE 0 WHEN THE DRIVER DID NOT SAY, which is not an error. Several
// drivers report neither, PostgreSQL answers the last-insert question with
// `RETURNING` instead, and failing a statement that RAN because its result
// cannot be counted would be reporting success as failure.
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

// Types is every data model this package knows, sorted.
func Types() []Type { return []Type{Document, KV, SQL, Vector} }

func known(t Type) bool { return slices.Contains(Types(), t) }

// Registry is which engine backs which type.
type Registry struct {
	mu sync.RWMutex

	// by type, then by engine name.
	engines map[Type]map[string]Engine
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{engines: map[Type]map[string]Engine{}}
}

// Register adds an engine, proving it can do what it claims.
//
// IT OPENS A PROBE CONNECTION PER TYPE. That is the only way to know the Conn an
// engine hands back implements the type's surface — the check cannot be done on
// the Engine, because Open's return type is the interface, not the concrete one.
// Doing it here means a mismatch is a startup failure naming the engine and the
// type, instead of a method-not-found on somebody's first query.
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

	// The probe is outside the lock: opening a connection may touch the
	// network, and holding the registry across it would block every other
	// registration and lookup on one slow server.
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

// probeSpec is a well-formed spec that names nowhere.
//
// IT CARRIES AN ADDRESS BECAUSE AN EXTERNAL ENGINE NEEDS ONE. The probe used to
// be `{Type, Engine, Name}` and nothing else, which is openable by an engine
// that runs here and by none that connects to a server — so postgres, mysql,
// mongo and every other external engine in the vocabulary could not be
// registered at all. The address is in the reserved `.invalid` TLD (RFC 2606),
// so it can never resolve and nothing can be reached through it by accident.
//
// WHICH MEANS AN ENGINE MUST NOT CONNECT IN Open. That is the ordinary shape —
// database/sql opens lazily and so does every client library worth using — and
// it is what makes this a proof of the SURFACE rather than a health check
// against a server that has nothing to do with whether the code is correct.
func probeSpec(t Type, engine string) Spec {
	return Spec{
		Type:     t,
		Engine:   engine,
		Name:     "probe",
		Address:  "probe.invalid:0",
		Database: "probe",
	}
}

// Open connects to what spec names.
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

// EnginesFor is every engine name registered for a type, sorted.
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
