// Package register holds compiled projects and is what the runtime reads on every
// invocation.
//
//	a user writes the v2 schema
//	  -> tcc compiles it
//	    -> the compiled object is published here
//	      -> the runtime reads it here, per resource and per environment
//
// Every runtime value that is not node configuration comes from that read.
//
// A compiled project is immutable and named by its content, so a reader may keep it
// forever. A pointer, one per (project, environment), names the object that
// environment runs plus its revision; it is small and the only thing a hot reader
// re-reads.
//
// A pointer refuses a revision not higher than its own: a late publish cannot roll
// an environment back, and a rollback is an ordinary publish of the previous object
// under a new revision.
//
// Storage and replication belong to the injected store; the bytes are tcc's output
// and this package does not parse them.
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
	"time"

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

	// ErrHostClaimed is a name another project already answers on. First claim
	// wins: otherwise an ordinary deploy could take another project's traffic.
	// Zone ownership is proved elsewhere, before a name is published.
	ErrHostClaimed = errors.New("register: another project already answers on this name")

	// ErrMalformedPointer is a pointer row that is present and unreadable, distinct
	// from ErrNotPublished so a broken store is not reported as a 404.
	ErrMalformedPointer = errors.New("register: a pointer that cannot be read")
)

// ObjectID is the content address of a compiled project: the hex sha256 of its
// bytes.
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

	// Revision is the deploy counter. It only ever increases.
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

	// List is every key under a prefix, in any order. The key grammar puts a
	// project's pointers under one prefix so its deployments are one range scan.
	List(ctx context.Context, prefix string) ([]string, error)
}

// The separator may not appear in a name, and that is checked: otherwise project
// "a/prod", environment "prod" and project "a", environment "prod/prod" share a row.
const (
	sep           = "/"
	objectPrefix  = "o" + sep
	pointerPrefix = "p" + sep
	hostPrefix    = "h" + sep
	claimsPrefix  = "c" + sep
)

// guarded is the injected store with the key grammar enforced on the way in.
// Keys are a fixed prefix plus outside text, and the stores clean keys with
// path.Clean, so ".." would escape the prefix:
//
//	hostKey("../p/victim/prod")  ->  "h/../p/victim/prod"  ->  /p/victim/prod
//
// No legitimate fqdn, project or environment holds such a segment.
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

// List checks the prefix without its trailing separator: "p/shop/" scopes the
// scan away from "p/shopX", and path.Clean would drop that slash.
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

// owner is the value under a host key. An empty value is an unclaimed name: the
// store has no Delete, so releasing a name writes a tombstone.
func owner(project, environment string) string { return project + sep + environment }

// Register holds compiled projects and what each environment points at.
type Register struct {
	store Store

	// mu makes a publish atomic against another publish of the same pointer.
	// The store is a key-value door with no compare-and-set, so the read of the
	// current revision and the write of the new one have to be one step here.
	mu sync.Mutex

	// now timestamps a link's claim, which is what orders two claims on one
	// project id (projects.go).
	now func() time.Time
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
	now := o.now
	if now == nil {
		now = time.Now
	}
	return &Register{store: guarded{o.store}, now: now}, nil
}

// Option configures a Register.
type Option func(*options) error

type options struct {
	store Store
	now   func() time.Time
}

// WithLinkClock is what a link's claim is timestamped with. The default is the
// wall clock; a test that orders two claims sets it.
func WithLinkClock(now func() time.Time) Option {
	return func(o *options) error {
		o.now = now
		return nil
	}
}

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

// Publish stores a compiled project and points an environment at it. The object
// goes first, so a reader that sees a pointer can always fetch what it names.
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

	// A malformed pointer does not block a publish: writing is where it gets fixed,
	// and the incoming revision has nothing to compare against.
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
	// The names before the pointer, so a deploy does not take effect for some requests
	// and not others. A refused claim leaves the environment on its current revision.
	if err := r.claim(ctx, project, environment, p.Hosts); err != nil {
		return "", err
	}
	// The views before the pointer too, so its destinations can read the revision.
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

	// Every claim is checked before any is written, so a refusal takes no names.
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

// ResolveHost is the read a gateway makes: whose is this name. It is
// case-insensitive, as DNS is.
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

// Publication is one publish.
type Publication struct {
	Project     string
	Environment string
	Revision    uint64

	// Compiled is tcc's output, opaque here.
	Compiled []byte

	// Hosts are the fully-qualified names this environment answers on. The register
	// does not derive them: that needs the vocabulary and the cloud's fqdn.
	Hosts []string

	// Views is the object each destination holds of this revision, opaque here as
	// Compiled is. Nil is a publication nobody cut: every destination reads the whole
	// object. An empty, non-nil list is a deployment cut for no destination at all.
	Views []View
}

// Resolve is the read the runtime makes: what does this environment run, and under
// which revision. It returns the pointer, since the object it names is immutable.
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

// Environments is every deployment of a project that has ever been published,
// sorted. A code build asks it first: which deployments a branch feeds is in the
// compiled object, which is read through a deployment name. A project nobody has
// published is an empty list.
func (r *Register) Environments(ctx context.Context, project string) ([]string, error) {
	// An empty project is the caller's mistake, not ErrMalformedPointer's data loss.
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
		// Cut wherever the prefix is: a store answers with its own normalised key
		// (`/pointers/shop/prod`), which a front-anchored trim would miss.
		_, name, found := strings.Cut(key, prefix)
		if !found {
			continue
		}
		// A separator left in the name means a longer project name sharing this prefix.
		if name == "" || strings.Contains(name, sep) {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
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

// A pointer is stored as "<revision> <object>". Nothing outside this package
// decodes it.
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

// Keys is every key held, sorted.
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
