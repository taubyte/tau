package project

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/register"
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

// Source is where compiled projects come from: the two reads a register offers,
// and no publish.
//
// A CONSUMER OF THIS CACHE CANNOT PUBLISH, and the interface is where that is
// enforced rather than in a comment. Both *register.Register and a register
// client satisfy it, so a node that runs both services wires them together with
// no network and nothing can tell.
type Source interface {
	Resolve(ctx context.Context, project, environment string) (register.Pointer, error)
	Object(ctx context.Context, id register.ObjectID) ([]byte, error)
}

var (
	ErrNoSource = errors.New("project: a cache with no source has nothing to read")

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
type Cache struct {
	source Source
	opts   cacheOptions

	// mu guards both maps. It is a plain mutex rather than an RWMutex: every
	// path that reads also writes — the pointer path refreshes, the project
	// path touches the LRU — so a read lock would be taken and immediately
	// upgraded.
	mu       sync.Mutex
	pointers map[string]pointerEntry
	projects map[register.ObjectID]*projectEntry
	lru      []register.ObjectID
}

type pointerEntry struct {
	pointer register.Pointer
	at      time.Time
}

// projectEntry is one cached compiled project. ready is closed when the load
// that claimed it has finished, so concurrent readers of the same object wait
// for one fetch rather than each starting their own — the fetch is a whole
// compiled project over the wire, and a deploy makes every in-flight request
// want it at the same instant.
type projectEntry struct {
	ready chan struct{}
	proj  *Project
	err   error
}

// NewCache builds a cache over a source.
func NewCache(source Source, opts ...CacheOption) (*Cache, error) {
	if source == nil {
		return nil, ErrNoSource
	}

	o := cacheOptions{ttl: DefaultTTL, max: DefaultMax, now: time.Now}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, err
		}
	}

	return &Cache{
		source:   source,
		opts:     o,
		pointers: map[string]pointerEntry{},
		projects: map[register.ObjectID]*projectEntry{},
	}, nil
}

// At is what an environment currently runs.
func (c *Cache) At(ctx context.Context, project, environment string) (*Project, register.Pointer, error) {
	pointer, err := c.pointer(ctx, project, environment)
	if err != nil {
		return nil, register.Pointer{}, err
	}
	p, err := c.object(ctx, pointer.Object)
	if err != nil {
		return nil, register.Pointer{}, err
	}
	return p, pointer, nil
}

// Held is how many compiled projects are cached. It exists for a test and for
// an operator asking what a node is holding.
func (c *Cache) Held() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.projects)
}

func (c *Cache) pointer(ctx context.Context, project, environment string) (register.Pointer, error) {
	key := project + "\x00" + environment

	c.mu.Lock()
	entry, ok := c.pointers[key]
	c.mu.Unlock()

	if ok && c.opts.now().Sub(entry.at) < c.opts.ttl {
		return entry.pointer, nil
	}

	p, err := c.source.Resolve(ctx, project, environment)
	if err != nil {
		return register.Pointer{}, err
	}

	c.mu.Lock()
	c.pointers[key] = pointerEntry{pointer: p, at: c.opts.now()}
	c.mu.Unlock()

	return p, nil
}

func (c *Cache) object(ctx context.Context, id register.ObjectID) (*Project, error) {
	c.mu.Lock()
	if entry, ok := c.projects[id]; ok {
		c.touch(id)
		c.mu.Unlock()
		<-entry.ready
		return entry.proj, entry.err
	}
	entry := &projectEntry{ready: make(chan struct{})}
	c.projects[id] = entry
	c.touch(id)
	c.evict()
	c.mu.Unlock()

	compiled, err := c.source.Object(ctx, id)
	if err == nil {
		entry.proj, err = Load(compiled)
	}
	entry.err = err
	close(entry.ready)

	// A FAILED LOAD IS NOT CACHED. The object is immutable, so a successful
	// load is good forever — but a failure is usually the register being
	// unreachable, and remembering it would turn one bad moment into a project
	// that never loads again.
	if err != nil {
		c.mu.Lock()
		if c.projects[id] == entry {
			delete(c.projects, id)
			c.forget(id)
		}
		c.mu.Unlock()
		return nil, err
	}
	return entry.proj, nil
}

// touch, forget and evict are the LRU. They are called with c.mu held.
func (c *Cache) touch(id register.ObjectID) {
	c.forget(id)
	c.lru = append(c.lru, id)
}

func (c *Cache) forget(id register.ObjectID) {
	for i, held := range c.lru {
		if held == id {
			c.lru = append(c.lru[:i], c.lru[i+1:]...)
			return
		}
	}
}

func (c *Cache) evict() {
	for len(c.lru) > c.opts.max {
		delete(c.projects, c.lru[0])
		c.lru = c.lru[1:]
	}
}
