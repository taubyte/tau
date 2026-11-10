package register

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// A compiled project is named by its content, so once fetched it is correct forever
// and only memory bounds it. A pointer moves, so it is asked of the registry every
// time: a registry that keeps it (services/register's Views keeps it until the
// register says it moved) is what makes that cheap, and a second copy kept here on a
// clock would only serve old code for as long as the clock ran.

// Registry is where compiled projects come from: the two reads a register offers,
// and no publish. Both *Register and a register client satisfy it.
type Registry interface {
	Resolve(ctx context.Context, project, environment string) (Pointer, error)
	Object(ctx context.Context, id ObjectID) ([]byte, error)
}

// Deployment is a project and one of its environments, the pair a pointer is
// resolved by. A type, so nobody has to split a joined string.
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
)

const (
	// DefaultTTL is the deploy propagation budget: how long an answer nothing
	// watches may be trusted.
	DefaultTTL = 5 * time.Second

	// DefaultMax is how many compiled projects are held. Old content addresses
	// stay valid forever, so correctness is not a bound; this is.
	DefaultMax = 64
)

// CacheOption configures a Cache.
type CacheOption func(*cacheOptions) error

type cacheOptions struct {
	max int
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

// Cache reads compiled projects and keeps them.
type Cache[T any] struct {
	registry Registry

	// parse turns an object's bytes into whatever the caller keeps. It is the
	// only thing about this cache that is not generic: everything else is a
	// pointer with a deadline and a content address with a budget.
	parse func([]byte) (T, error)
	opts  cacheOptions

	// A hit takes no lock: every request reads the table, and a mutex there held
	// every core to one. seen is every Deployment asked for, the hint Deployments
	// gives. objects is an ObjectID's *objectEntry; mu serialises what changes it, a
	// load claiming an id and the eviction that may follow, and guards held.
	seen    sync.Map
	objects sync.Map
	mu      sync.Mutex
	held    int

	// used is the LRU's clock. An entry carries the stamp of its last use, unique, so
	// the least recently used is the smallest; a hit on the entry already most
	// recent writes nothing.
	used atomic.Uint64
}

// objectEntry is one cached compiled project. ready is closed when the load that
// claimed it finishes, so concurrent readers wait for one fetch: a deploy makes every
// in-flight request want the same project at once. loaded says so without the channel,
// whose receive takes its lock even once closed.
type objectEntry[T any] struct {
	ready  chan struct{}
	loaded atomic.Bool
	proj   T
	err    error
	used   atomic.Uint64
}

// NewCache builds a cache over a registry.
func NewCache[T any](registry Registry, parse func([]byte) (T, error), opts ...CacheOption) (*Cache[T], error) {
	if registry == nil {
		return nil, ErrNoRegistry
	}
	if parse == nil {
		return nil, ErrNoParse
	}

	o := cacheOptions{max: DefaultMax}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, err
		}
	}

	return &Cache[T]{registry: registry, parse: parse, opts: o}, nil
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

// Held is how many compiled projects are cached.
func (c *Cache[T]) Held() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.held
}

func (c *Cache[T]) pointer(ctx context.Context, at Deployment) (Pointer, error) {
	p, err := c.registry.Resolve(ctx, at.Project, at.Environment)
	if err != nil {
		return Pointer{}, err
	}
	if _, ok := c.seen.Load(at); !ok {
		c.seen.Store(at, struct{}{})
	}
	return p, nil
}

func (c *Cache[T]) object(ctx context.Context, id ObjectID) (T, error) {
	if held, ok := c.objects.Load(id); ok {
		return c.read(held.(*objectEntry[T]))
	}

	c.mu.Lock()
	if held, ok := c.objects.Load(id); ok {
		c.mu.Unlock()
		return c.read(held.(*objectEntry[T]))
	}
	entry := &objectEntry[T]{ready: make(chan struct{})}
	c.touch(entry)
	c.objects.Store(id, entry)
	c.held++
	c.evict()
	c.mu.Unlock()

	compiled, err := c.registry.Object(ctx, id)
	if err == nil {
		entry.proj, err = c.parse(compiled)
	}
	entry.err = err
	entry.loaded.Store(true)
	close(entry.ready)

	// A failed load is not cached: it is usually the register being unreachable, and
	// remembering it would make the project never load again.
	if err != nil {
		c.mu.Lock()
		if held, ok := c.objects.Load(id); ok && held == entry {
			c.objects.Delete(id)
			c.held--
		}
		c.mu.Unlock()
		var zero T
		return zero, err
	}
	return entry.proj, nil
}

// read waits for an entry another caller is loading, and counts the use.
func (c *Cache[T]) read(entry *objectEntry[T]) (T, error) {
	c.touch(entry)
	if !entry.loaded.Load() {
		<-entry.ready
	}
	return entry.proj, entry.err
}

func (c *Cache[T]) touch(entry *objectEntry[T]) {
	if latest := c.used.Load(); latest == 0 || entry.used.Load() != latest {
		entry.used.Store(c.used.Add(1))
	}
}

// evict drops the least recently used until the budget holds. It is called with c.mu
// held, on a load, so its scan of the at most max+1 entries is a load's cost.
func (c *Cache[T]) evict() {
	for c.held > c.opts.max {
		var oldest any
		least := uint64(0)
		c.objects.Range(func(id, held any) bool {
			if used := held.(*objectEntry[T]).used.Load(); oldest == nil || used < least {
				oldest, least = id, used
			}
			return true
		})
		c.objects.Delete(oldest)
		c.held--
	}
}

// Deployments is every (project, environment) this cache has been asked for. A
// router may send traffic where it is already warm; it is a hint, never a rule, or
// a project nobody has loaded would have nowhere to go.
func (c *Cache[T]) Deployments() []Deployment {
	var out []Deployment
	c.seen.Range(func(at, _ any) bool {
		out = append(out, at.(Deployment))
		return true
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Environment < out[j].Environment
	})
	return out
}
