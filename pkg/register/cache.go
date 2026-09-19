package register

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// cache.go keeps compiled projects, and it is here rather than in each consumer
// because every consumer needs exactly this and none of them differs.
//
// TWO CACHES, CACHED FOR OPPOSITE REASONS. A compiled project is named by its
// content, so one that has been fetched is correct forever and the only bound
// on holding it is memory. A pointer is the thing that MOVES, so it is held for
// as long as a deploy is allowed to take to propagate and no longer. Collapsing
// them would mean either re-fetching a whole compiled project every few seconds
// or serving replaced code indefinitely.
//
// A worker and a gateway both do this on every request. Two copies of it would
// be two answers to "how long after a deploy does the old code stop being
// served", which is a promise a platform makes once.

// Registry is where compiled projects come from: the two reads a register offers,
// and no publish.
//
// A CONSUMER OF THIS CACHE CANNOT PUBLISH, and the interface is where that is
// enforced rather than in a comment. Both *Register and a register
// client satisfy it, so a node that runs both services wires them together with
// no network and nothing can tell.
type Registry interface {
	Resolve(ctx context.Context, project, environment string) (Pointer, error)
	Object(ctx context.Context, id ObjectID) ([]byte, error)
}

// Deployment is a project and one of its environments — the pair a pointer is
// resolved by, and the key this cache holds one under.
//
// IT IS A TYPE BECAUSE IT IS A PAIR. Joined into a string it has to be taken
// apart again by whoever reads it, and where that seam existed the two ends
// disagreed about the separator.
type Deployment struct {
	Project     string
	Environment string
}

// String is "project/environment", for a log line and for a name a human types.
// Nothing parses it back.
func (d Deployment) String() string { return d.Project + "/" + d.Environment }

var (
	ErrNoParse    = errors.New("register: a cache with no way to read an object cannot hold one")
	ErrNoRegistry = errors.New("project: a cache with nothing to read from has nothing to answer")

	// DefaultTTL is how long a resolved pointer is trusted. It IS the deploy
	// propagation budget and it is the only thing it is: the object a pointer
	// names is immutable, so this number does not change how much work a
	// request does — only how long after a deploy the old code keeps answering.
	DefaultTTL = 5 * time.Second

	// DefaultMax is how many compiled projects are held. Every deploy mints a
	// new content address and the old one stays valid forever, so "still
	// correct" is not a bound; this is.
	DefaultMax = 64
)

// CacheOption configures a Cache.
type CacheOption func(*cacheOptions) error

type cacheOptions struct {
	ttl time.Duration
	max int
	now func() time.Time
}

// WithTTL is how long a resolved pointer is trusted.
func WithTTL(d time.Duration) CacheOption {
	return func(o *cacheOptions) error {
		if d < 0 {
			return fmt.Errorf("project: ttl %s: a negative deploy budget is not one", d)
		}
		o.ttl = d
		return nil
	}
}

// WithMax bounds how many compiled projects are held.
func WithMax(n int) CacheOption {
	return func(o *cacheOptions) error {
		if n < 1 {
			return fmt.Errorf("project: %d projects: a cache that holds nothing reloads on every request", n)
		}
		o.max = n
		return nil
	}
}

// WithClock injects the local clock. Tests use it; production does not.
func WithClock(now func() time.Time) CacheOption {
	return func(o *cacheOptions) error {
		if now == nil {
			return errors.New("project: a nil clock")
		}
		o.now = now
		return nil
	}
}

// Cache reads compiled projects and keeps them.
type Cache[T any] struct {
	registry Registry

	// parse turns an object's bytes into whatever the caller keeps. It is the
	// only thing about this cache that is not generic: everything else is a
	// pointer with a deadline and a content address with a budget.
	parse func([]byte) (T, error)
	opts  cacheOptions

	// mu guards both maps. It is a plain mutex rather than an RWMutex: every
	// path that reads also writes — the pointer path refreshes, the project
	// path touches the LRU — so a read lock would be taken and immediately
	// upgraded.
	mu       sync.Mutex
	pointers map[Deployment]pointerEntry
	objects  map[ObjectID]*objectEntry[T]
	lru      []ObjectID
}

type pointerEntry struct {
	pointer Pointer
	at      time.Time
}

// projectEntry is one cached compiled project. ready is closed when the load
// that claimed it has finished, so concurrent readers of the same object wait
// for one fetch rather than each starting their own — the fetch is a whole
// compiled project over the wire, and a deploy makes every in-flight request
// want it at the same instant.
type objectEntry[T any] struct {
	ready chan struct{}
	proj  T
	err   error
}

// NewCache builds a cache over a registry.
func NewCache[T any](registry Registry, parse func([]byte) (T, error), opts ...CacheOption) (*Cache[T], error) {
	if registry == nil {
		return nil, ErrNoRegistry
	}
	if parse == nil {
		return nil, ErrNoParse
	}

	o := cacheOptions{ttl: DefaultTTL, max: DefaultMax, now: time.Now}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, err
		}
	}

	return &Cache[T]{
		registry: registry,
		parse:    parse,
		opts:     o,
		pointers: map[Deployment]pointerEntry{},
		objects:  map[ObjectID]*objectEntry[T]{},
	}, nil
}

// At is what an environment currently runs.
func (c *Cache[T]) At(ctx context.Context, at Deployment) (T, Pointer, error) {
	pointer, err := c.pointer(ctx, at)
	if err != nil {
		var zero T
		return zero, Pointer{}, err
	}
	p, err := c.object(ctx, pointer.Object)
	if err != nil {
		var zero T
		return zero, Pointer{}, err
	}
	return p, pointer, nil
}

// Held is how many compiled projects are cached. It exists for a test and for
// an operator asking what a node is holding.
func (c *Cache[T]) Held() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.objects)
}

func (c *Cache[T]) pointer(ctx context.Context, at Deployment) (Pointer, error) {

	c.mu.Lock()
	entry, ok := c.pointers[at]
	c.mu.Unlock()

	if ok && c.opts.now().Sub(entry.at) < c.opts.ttl {
		return entry.pointer, nil
	}

	p, err := c.registry.Resolve(ctx, at.Project, at.Environment)
	if err != nil {
		return Pointer{}, err
	}

	c.mu.Lock()
	c.pointers[at] = pointerEntry{pointer: p, at: c.opts.now()}
	c.mu.Unlock()

	return p, nil
}

func (c *Cache[T]) object(ctx context.Context, id ObjectID) (T, error) {
	c.mu.Lock()
	if entry, ok := c.objects[id]; ok {
		c.touch(id)
		c.mu.Unlock()
		<-entry.ready
		return entry.proj, entry.err
	}
	entry := &objectEntry[T]{ready: make(chan struct{})}
	c.objects[id] = entry
	c.touch(id)
	c.evict()
	c.mu.Unlock()

	compiled, err := c.registry.Object(ctx, id)
	if err == nil {
		entry.proj, err = c.parse(compiled)
	}
	entry.err = err
	close(entry.ready)

	// A FAILED LOAD IS NOT CACHED. The object is immutable, so a successful
	// load is good forever — but a failure is usually the register being
	// unreachable, and remembering it would turn one bad moment into a project
	// that never loads again.
	if err != nil {
		c.mu.Lock()
		if c.objects[id] == entry {
			delete(c.objects, id)
			c.forget(id)
		}
		c.mu.Unlock()
		var zero T
		return zero, err
	}
	return entry.proj, nil
}

// touch, forget and evict are the LRU. They are called with c.mu held.
func (c *Cache[T]) touch(id ObjectID) {
	c.forget(id)
	c.lru = append(c.lru, id)
}

func (c *Cache[T]) forget(id ObjectID) {
	for i, held := range c.lru {
		if held == id {
			c.lru = append(c.lru[:i], c.lru[i+1:]...)
			return
		}
	}
}

func (c *Cache[T]) evict() {
	for len(c.lru) > c.opts.max {
		delete(c.objects, c.lru[0])
		c.lru = c.lru[1:]
	}
}

// Deployments is every (project, environment) this cache currently holds a
// pointer for.
//
// IT IS WHAT A NODE CAN ANSWER FOR CHEAPLY. A worker that already holds an
// environment's compiled object serves a request for it without a fetch, so a
// router that knows which nodes hold what can send traffic where it is already
// warm — and that is the whole reason this is exposed rather than kept private.
//
// It is a HINT and never a rule: a node that holds nothing must still be
// reachable, or a project nobody has loaded yet would have nowhere to go.
//
// IT ANSWERS PAIRS AND NOT STRINGS. It used to join them with a "/" — after the
// map had already joined them with a "\x00" — and the one consumer scanned
// backwards for the separator to take them apart again. Three spellings of one
// address, none of which a compiler was checking.
func (c *Cache[T]) Deployments() []Deployment {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]Deployment, 0, len(c.pointers))
	for at := range c.pointers {
		out = append(out, at)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Environment < out[j].Environment
	})
	return out
}
