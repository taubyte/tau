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

	"github.com/taubyte/tau/pkg/storekey"
)

var (
	ErrNoProject     = errors.New("register: a pointer with no project addresses nothing")
	ErrNoEnvironment = errors.New("register: a pointer with no environment addresses nothing")
	ErrEmptyObject   = errors.New("register: an empty compiled project is not a project")
	ErrNotPublished  = errors.New("register: nothing is published for this environment")
	ErrNoHost        = errors.New("register: nothing answers on this name")
	ErrNoObject      = errors.New("register: no such object")
	ErrStaleRevision = errors.New("register: a revision that does not move forward")
	ErrSeparator     = errors.New("register: a name may not contain the key separator")

	// ErrHostClaimed is a name another project already answers on.
	//
	// FIRST CLAIM WINS, AND A LATER ONE IS REFUSED. Two projects on one cloud
	// can both write `shop.example.com` into their config, and whichever
	// deployed second would otherwise take the first one's traffic — which is
	// a takeover performed by an ordinary deploy. That the claimant owns the
	// zone at all is a different question and not this package's: it is proved
	// once, elsewhere, before a name is ever published.
	ErrHostClaimed = errors.New("register: another project already answers on this name")

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

	// List is every key under a prefix, in any order.
	//
	// IT IS A PREFIX AND NOT A QUERY, which is the whole of what this state
	// machine ever asks of a store: the key grammar puts a project's pointers
	// under one prefix on purpose, so "which deployments does this project
	// have" is a scan of one range rather than an index somebody has to keep
	// in step with the pointers.
	//
	// A store that cannot enumerate is not usable here, and that is a
	// deliberate widening: the alternative was a second index of environment
	// names beside the pointers, written by the same writer, which is one more
	// thing that can disagree with the thing it describes.
	List(ctx context.Context, prefix string) ([]string, error)
}

// keys. THE SEPARATOR MAY NOT APPEAR IN A NAME, and that is checked rather than
// assumed: a project called "a/prod" and an environment called "prod" would
// otherwise address the same row as project "a", environment "prod/prod", and
// one deploy would silently overwrite another project's.
const (
	sep           = "/"
	objectPrefix  = "o" + sep
	pointerPrefix = "p" + sep
	hostPrefix    = "h" + sep
	claimsPrefix  = "c" + sep
)

// guarded is the injected store with the key grammar enforced on the way in.
//
// EVERY KEY HERE IS A FIXED PREFIX PLUS SOMEBODY ELSE'S TEXT -- a fqdn out of a
// Host header, a project and an environment out of a publication -- and the
// stores this runs over put the key through ds.NewKey, which is path.Clean. So
// a name holding ".." collapsed the prefix away before the row was touched:
//
//	hostKey("../p/victim/prod")  ->  "h/../p/victim/prod"  ->  /p/victim/prod
//
// which READS another project's pointer on a request whose Host header said so,
// and WRITES one on a publish whose project declared that name. A legitimate
// fqdn, project or environment holds no such segment, so nothing addressable is
// lost by refusing it.
//
// IT IS ONE WRAPPER AND NOT A CHECK IN EACH BUILDER. There are four builders
// and fourteen call sites; a rule spelled at each of them is a rule the
// fifteenth forgets.
type guarded struct{ Store }

func (g guarded) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := storekey.Canonical(key); err != nil {
		return nil, false, err
	}
	return g.Store.Get(ctx, key)
}

func (g guarded) Put(ctx context.Context, key string, value []byte) error {
	if err := storekey.Canonical(key); err != nil {
		return err
	}
	return g.Store.Put(ctx, key, value)
}

// List checks the prefix WITHOUT its trailing separator, which is load-bearing
// rather than cosmetic: "p/shop/" is how a scan says shop's deployments and not
// shopX's, and path.Clean drops that slash. Trimming one before asking keeps
// the boundary the caller meant while still refusing a prefix that traverses.
func (g guarded) List(ctx context.Context, prefix string) ([]string, error) {
	if err := storekey.Canonical(strings.TrimSuffix(prefix, sep)); err != nil {
		return nil, err
	}
	return g.Store.List(ctx, prefix)
}

func objectKey(id ObjectID) string { return objectPrefix + string(id) }

func pointerKey(project, environment string) string {
	return pointerPrefix + project + sep + environment
}

// hostKey holds who answers on a name; claimsKey holds which names one
// environment claimed last time, so a republish can release the ones it dropped.
func hostKey(fqdn string) string { return hostPrefix + strings.ToLower(fqdn) }

func claimsKey(project, environment string) string {
	return claimsPrefix + project + sep + environment
}

// owner is the value under a host key. An EMPTY value is an unclaimed name
// rather than a missing row: the store has Get and Put and no Delete, so
// releasing a name is writing a tombstone over it. That is also the honest
// shape for a replicated store, where a deletion is a tombstone anyway.
func owner(project, environment string) string { return project + sep + environment }

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
	return &Register{store: guarded{o.store}}, nil
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
func (r *Register) Publish(ctx context.Context, p Publication) (ObjectID, error) {
	if err := checkName(p.Project, ErrNoProject); err != nil {
		return "", err
	}
	if err := checkName(p.Environment, ErrNoEnvironment); err != nil {
		return "", err
	}
	if len(p.Compiled) == 0 {
		return "", ErrEmptyObject
	}

	project, environment, revision, compiled := p.Project, p.Environment, p.Revision, p.Compiled
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
	// THE NAMES BEFORE THE POINTER, for the same reason as the object: a
	// pointer that moved while the names still sent traffic somewhere else
	// would be a deploy that took effect for some requests and not others. A
	// refused claim leaves the environment on the revision it was already
	// running.
	if err := r.claim(ctx, project, environment, p.Hosts); err != nil {
		return "", err
	}
	// THE VIEWS BEFORE THE POINTER too: a pointer that moved ahead of its manifest
	// would be a revision its destinations could not read yet.
	if err := r.publishViews(ctx, p, id); err != nil {
		return "", err
	}

	if err := r.store.Put(ctx, pointerKey(project, environment), encodePointer(Pointer{Object: id, Revision: revision})); err != nil {
		return "", fmt.Errorf("register: moving %s/%s: %w", project, environment, err)
	}
	return id, nil
}

// claim takes the names this publish declares and releases the ones the last
// one held and this one does not.
func (r *Register) claim(ctx context.Context, project, environment string, hosts []string) error {
	mine := owner(project, environment)

	held, err := r.claims(ctx, project, environment)
	if err != nil {
		return err
	}

	wanted := map[string]bool{}
	for _, h := range hosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			wanted[h] = true
		}
	}

	// EVERY CLAIM IS CHECKED BEFORE ANY IS WRITTEN. A publish that took half
	// the names and then hit one somebody else holds would leave the cloud
	// answering for a deploy that did not happen.
	for _, h := range sortedKeys(wanted) {
		at, found, err := r.store.Get(ctx, hostKey(h))
		if err != nil {
			return fmt.Errorf("register: reading the claim on %s: %w", h, err)
		}
		if found && len(at) > 0 && string(at) != mine {
			return fmt.Errorf("%w: %s is %s's", ErrHostClaimed, h, at)
		}
	}

	for _, h := range sortedKeys(wanted) {
		if err := r.store.Put(ctx, hostKey(h), []byte(mine)); err != nil {
			return fmt.Errorf("register: claiming %s: %w", h, err)
		}
	}
	for _, h := range held {
		if wanted[h] {
			continue
		}
		// A tombstone rather than a deletion: see owner().
		if err := r.store.Put(ctx, hostKey(h), nil); err != nil {
			return fmt.Errorf("register: releasing %s: %w", h, err)
		}
	}

	return r.store.Put(ctx, claimsKey(project, environment), []byte(strings.Join(sortedKeys(wanted), "\n")))
}

func (r *Register) claims(ctx context.Context, project, environment string) ([]string, error) {
	b, found, err := r.store.Get(ctx, claimsKey(project, environment))
	if err != nil {
		return nil, fmt.Errorf("register: reading %s/%s's names: %w", project, environment, err)
	}
	if !found || len(b) == 0 {
		return nil, nil
	}
	return strings.Split(string(b), "\n"), nil
}

// ResolveHost is the read a gateway makes: a request arrived for this name, so
// whose is it.
//
// IT IS CASE-INSENSITIVE because DNS is, and a Host header carries whatever the
// client typed. A table keyed by what the author happened to write would miss
// half the requests for the same name.
func (r *Register) ResolveHost(ctx context.Context, fqdn string) (project, environment string, err error) {
	fqdn = strings.ToLower(strings.TrimSpace(fqdn))
	if fqdn == "" {
		return "", "", ErrNoHost
	}

	at, found, err := r.store.Get(ctx, hostKey(fqdn))
	if err != nil {
		return "", "", fmt.Errorf("register: reading the claim on %s: %w", fqdn, err)
	}
	if !found || len(at) == 0 {
		return "", "", fmt.Errorf("%w: %s", ErrNoHost, fqdn)
	}

	project, environment, ok := strings.Cut(string(at), sep)
	if !ok || project == "" || environment == "" {
		return "", "", fmt.Errorf("%w: %q claims %s", ErrMalformedPointer, at, fqdn)
	}
	return project, environment, nil
}

// Publication is one publish. It is a value rather than five parameters because
// the names an environment answers on are part of what a deploy declares, and a
// sixth positional argument is where a caller starts passing them in the wrong
// order.
type Publication struct {
	Project     string
	Environment string
	Revision    uint64

	// Compiled is tcc's output, opaque here.
	Compiled []byte

	// Hosts are the fully-qualified names this environment answers on.
	//
	// THE REGISTER DOES NOT DERIVE THEM, and that is the same rule as not
	// parsing the object: a name is the label a resource chose, the zone that
	// answers for it and — for a generated one — the cloud's own fqdn, which is
	// not in the config at all. Deriving them here would put the vocabulary and
	// the operator's cloud name inside a package that is meant to hold neither.
	Hosts []string

	// Views is the object each destination holds of this revision, opaque here as
	// Compiled is. NIL IS A PUBLICATION NOBODY CUT: every destination reads the whole
	// object. An empty, non-nil list is a deployment cut for no destination at all.
	// See views.go.
	Views []View
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
// Environments is every deployment of a project that has ever been published,
// sorted.
//
// IT IS WHAT A CODE BUILD ASKS FIRST. A push to a code repository knows a
// project and a branch; which deployments that branch feeds is in the compiled
// object, and reading the compiled object needs the name of a deployment to
// read it from. This is that name.
//
// A project nobody has published is an empty list and not an error: the
// question "which deployments does it have" has an answer, and the answer is
// none.
func (r *Register) Environments(ctx context.Context, project string) ([]string, error) {
	// THE CALLER'S MISTAKE IS NOT DATA LOSS. ErrMalformedPointer is a stored
	// row that is present and unreadable, and reporting a bad argument with it
	// would tell a caller who sent an empty project that this register had lost
	// something.
	if project == "" {
		return nil, ErrNoProject
	}
	if strings.Contains(project, sep) {
		return nil, fmt.Errorf("%w: project %q", ErrSeparator, project)
	}

	prefix := pointerPrefix + project + sep
	keys, err := r.store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("register: listing %s's deployments: %w", project, err)
	}

	out := make([]string, 0, len(keys))
	for _, key := range keys {
		// THE PREFIX IS CUT WHEREVER IT IS AND NOT TRIMMED FROM THE FRONT. A
		// store answers a listing with the key it holds, which it normalised
		// into a datastore path — so what comes back is `/pointers/shop/prod`
		// and a front-anchored trim of `pointers/shop/` matches nothing, leaves
		// the whole key, and the separator check below then drops every
		// environment this project has. That is this function returning empty
		// on a real store while its test passed against a double that answered
		// the un-normalised key.
		_, name, found := strings.Cut(key, prefix)
		if !found {
			continue
		}
		// A KEY WITH A SEPARATOR LEFT IN IT IS NOT ONE OF THIS PROJECT'S. The
		// grammar refuses the separator inside a name, so anything still
		// carrying one under this prefix belongs to a longer project name that
		// happens to start the same way.
		if name == "" || strings.Contains(name, sep) {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

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

func (s *MemStore) List(_ context.Context, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []string
	for k := range s.m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
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

// sortedKeys is a stable order for a set, so a claim list and the writes that
// build it read the same way every time.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
