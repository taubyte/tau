// Package register is where a compiled project lives and what the runtime reads
// on every invocation.
//
// # The chain this is the end of
//
//	a user writes the v2 schema
//	  -> tcc COMPILES it
//	    -> the compiled object is published HERE
//	      -> the runtime reads it here, per resource and per environment
//
// Every runtime value that is not node configuration comes from that read.
// `execution/memory` and `execution/timeout` are Required() and PerEnv() in
// pkg/tcc/taubyte/v2/schema; they are not operator settings and not constructor
// arguments, and this is the only place they can come from.
//
// v1 called this tns. The name said name resolution; the job is holding what has
// been published and answering lookups, which is a register.
//
// # Two things, and only one of them changes
//
// A COMPILED PROJECT IS IMMUTABLE AND NAMED BY ITS CONTENT. Publishing the same
// configuration twice produces the same id, and an id names one object for all
// time. So a reader that has an object may keep it forever and never ask again —
// which is what makes a read on every invocation affordable.
//
// A POINTER IS WHAT MOVES. One per (project, environment), naming the object
// that environment currently runs, plus the revision that put it there. It is
// small, and it is the only thing a hot reader has to re-read.
//
// That split is the whole design. Without it the runtime would re-fetch a whole
// compiled project to answer "how much memory does this function get", on every
// call, forever.
//
// # A revision never goes backwards
//
// Revisions are the deploy counter, and a pointer refuses one that is not higher
// than the one it holds. Two consequences, both wanted:
//
//   - a publish that arrives late — a retry, a node catching up, a replayed
//     message — cannot roll an environment back to code somebody already
//     replaced;
//   - a ROLLBACK is an ordinary publish of the previous object under a NEW
//     revision, so "go back to what we ran on Tuesday" is a forward operation
//     and leaves a trail, rather than a reversal that erases what happened.
//
// # What is not decided here
//
// HOW OBJECTS AND POINTERS ARE STORED OR REPLICATED. This is a state machine
// over an injected store, so what it means to publish and resolve is a unit
// test rather than a cluster fixture. The consistency a pointer needs is a
// deployment question and the store answers it.
//
// WHAT IS INSIDE AN OBJECT. The bytes are tcc's compiled output and this package
// does not parse them. A register that understood the vocabulary would have to
// change every time the vocabulary did, and every consumer would then be reading
// this package's idea of the schema instead of the schema.
package register

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrNoProject     = errors.New("register: a pointer with no project addresses nothing")
	ErrNoEnvironment = errors.New("register: a pointer with no environment addresses nothing")
	ErrEmptyObject   = errors.New("register: an empty compiled project is not a project")
	ErrNotPublished  = errors.New("register: nothing is published for this environment")
	ErrNoObject      = errors.New("register: no such object")
	ErrStaleRevision = errors.New("register: a revision that does not move forward")
	ErrSeparator     = errors.New("register: a name may not contain the key separator")

	// ErrMalformedPointer is a pointer row that is present and unreadable.
	//
	// IT IS NOT ErrNotPublished, and the difference is the point: a runtime
	// told "not published" would answer 404 for an environment that has been
	// serving for months, and nothing in that answer says the store is the
	// broken part.
	ErrMalformedPointer = errors.New("register: a pointer that cannot be read")
)

// ObjectID is the content address of a compiled project: the hex sha256 of its
// bytes. Two identical compilations produce the same id, which is what lets a
// reader cache one forever.
type ObjectID string

// IDOf is the content address of these bytes.
func IDOf(compiled []byte) ObjectID {
	sum := sha256.Sum256(compiled)
	return ObjectID(hex.EncodeToString(sum[:]))
}

// Pointer is what one environment of one project currently runs.
type Pointer struct {
	// Object is the compiled project in force.
	Object ObjectID

	// Revision is the deploy counter. It only ever increases — see the package
	// doc on why a rollback is a forward publish.
	Revision uint64
}

// Store is the slice of persistence this package needs, declared here and as
// narrow as the use so every test below runs against a map.
//
// IT IS NOT A DATABASE INTERFACE. There is no query, no iteration and no
// transaction: a register reads one pointer or one object at a time by an exact
// key, and anything wider would be a door this package does not need and a
// consumer would have to implement.
//
// GET SEPARATES "NOT THERE" FROM "COULD NOT LOOK". A store that answered both
// with a bare miss would make Resolve say "nothing is published" while the
// store was unreachable, and an environment that has been running for months
// would read as never deployed. Those two answers make a caller do opposite
// things, so they are two results.
//
// BOTH TAKE A CONTEXT because a production store is a replicated one on the
// other side of the swarm, and this is called from a request that has a
// deadline. A store call that could not be abandoned would outlive the request
// that wanted it.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte) error
}

// keys. THE SEPARATOR MAY NOT APPEAR IN A NAME, and that is checked rather than
// assumed: a project called "a/prod" and an environment called "prod" would
// otherwise address the same row as project "a", environment "prod/prod", and
// one deploy would silently overwrite another project's.
const (
	sep           = "/"
	objectPrefix  = "o" + sep
	pointerPrefix = "p" + sep
)

func objectKey(id ObjectID) string { return objectPrefix + string(id) }

func pointerKey(project, environment string) string {
	return pointerPrefix + project + sep + environment
}

// Register holds compiled projects and what each environment points at.
type Register struct {
	store Store

	// mu makes a publish atomic against another publish of the same pointer.
	// The store is a key-value door with no compare-and-set, so the read of the
	// current revision and the write of the new one have to be one step here.
	mu sync.Mutex
}

// New builds a register over a store.
func New(opts ...Option) (*Register, error) {
	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, err
		}
	}
	if o.store == nil {
		return nil, errors.New("register: no store to publish into")
	}
	return &Register{store: o.store}, nil
}

// Option configures a Register.
type Option func(*options) error

type options struct{ store Store }

// WithStore is where objects and pointers live. Required.
func WithStore(s Store) Option {
	return func(o *options) error {
		if s == nil {
			return errors.New("register: no store to publish into")
		}
		o.store = s
		return nil
	}
}

// Publish stores a compiled project and points an environment at it.
//
// IT IS TWO WRITES AND THE ORDER MATTERS. The object goes first, so a reader
// that sees a pointer can always fetch what it names. A pointer that arrived
// before its object would be an environment that resolves to nothing, which is
// worse than one that has not been updated yet.
func (r *Register) Publish(ctx context.Context, project, environment string, revision uint64, compiled []byte) (ObjectID, error) {
	if err := checkName(project, ErrNoProject); err != nil {
		return "", err
	}
	if err := checkName(environment, ErrNoEnvironment); err != nil {
		return "", err
	}
	if len(compiled) == 0 {
		return "", ErrEmptyObject
	}

	id := IDOf(compiled)

	r.mu.Lock()
	defer r.mu.Unlock()

	// A MALFORMED POINTER DOES NOT BLOCK A PUBLISH. Reading is where a broken
	// row must be reported; writing is where it gets fixed. Refusing here
	// would leave an environment that can only be repaired by hand, and the
	// incoming revision is authoritative either way — there is nothing to
	// compare it against.
	current, ok, err := r.pointer(ctx, project, environment)
	if err != nil && !errors.Is(err, ErrMalformedPointer) {
		return "", err
	}
	if ok && revision <= current.Revision {
		return "", fmt.Errorf("%w: %s/%s holds %d, was offered %d",
			ErrStaleRevision, project, environment, current.Revision, revision)
	}

	// The object first. Writing the same object twice is free and idempotent:
	// its key is its content.
	if err := r.store.Put(ctx, objectKey(id), compiled); err != nil {
		return "", fmt.Errorf("register: storing the compiled project: %w", err)
	}
	if err := r.store.Put(ctx, pointerKey(project, environment), encodePointer(Pointer{Object: id, Revision: revision})); err != nil {
		return "", fmt.Errorf("register: moving %s/%s: %w", project, environment, err)
	}
	return id, nil
}

// Resolve is the read the runtime makes: what does this environment run, and
// under which revision.
//
// It returns the POINTER and not the object, because the pointer is the part
// that changes. A caller that already holds the object this names has nothing
// left to do — that is the whole reason objects are content-addressed.
func (r *Register) Resolve(ctx context.Context, project, environment string) (Pointer, error) {
	if err := checkName(project, ErrNoProject); err != nil {
		return Pointer{}, err
	}
	if err := checkName(environment, ErrNoEnvironment); err != nil {
		return Pointer{}, err
	}
	p, ok, err := r.pointer(ctx, project, environment)
	if err != nil {
		return Pointer{}, err
	}
	if !ok {
		return Pointer{}, fmt.Errorf("%w: %s/%s", ErrNotPublished, project, environment)
	}
	return p, nil
}

// Object is one compiled project by its content address.
func (r *Register) Object(ctx context.Context, id ObjectID) ([]byte, error) {
	if id == "" {
		return nil, ErrNoObject
	}
	b, ok, err := r.store.Get(ctx, objectKey(id))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoObject, id)
	}
	return b, nil
}

// pointer reads one pointer. A row that is present but unreadable is an ERROR
// and not a miss: it is a corrupted deploy, and answering "not published" would
// send the caller to redeploy over a store that is broken underneath.
func (r *Register) pointer(ctx context.Context, project, environment string) (Pointer, bool, error) {
	b, ok, err := r.store.Get(ctx, pointerKey(project, environment))
	if err != nil {
		return Pointer{}, false, fmt.Errorf("register: reading %s/%s: %w", project, environment, err)
	}
	if !ok {
		return Pointer{}, false, nil
	}
	p, err := decodePointer(b)
	if err != nil {
		return Pointer{}, false, err
	}
	return p, true, nil
}

func checkName(s string, empty error) error {
	if s == "" {
		return empty
	}
	if strings.Contains(s, sep) {
		return fmt.Errorf("%w: %q", ErrSeparator, s)
	}
	return nil
}

// A pointer is stored as "<revision> <object>": two fields, no library, and
// readable in a store dump. It is not protobuf because nothing outside this
// package decodes it — what crosses the wire is the RPC surface, not this row.
func encodePointer(p Pointer) []byte {
	return fmt.Appendf(nil, "%d %s", p.Revision, p.Object)
}

func decodePointer(b []byte) (Pointer, error) {
	rev, obj, ok := strings.Cut(string(b), " ")
	if !ok {
		return Pointer{}, fmt.Errorf("%w: %q", ErrMalformedPointer, b)
	}
	var r uint64
	if _, err := fmt.Sscanf(rev, "%d", &r); err != nil {
		return Pointer{}, fmt.Errorf("%w: revision %q", ErrMalformedPointer, rev)
	}
	if obj == "" {
		return Pointer{}, fmt.Errorf("%w: it names no object", ErrMalformedPointer)
	}
	return Pointer{Object: ObjectID(obj), Revision: r}, nil
}

// MemStore is an in-memory Store, for tests and for a single-node development
// register. It is safe for concurrent use.
type MemStore struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func NewMemStore() *MemStore { return &MemStore{m: map[string][]byte{}} }

func (s *MemStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

func (s *MemStore) Put(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = append([]byte(nil), value...)
	return nil
}

// Keys is every key held, sorted. It exists for tests and for a dump; nothing
// in this package's own logic iterates.
func (s *MemStore) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
