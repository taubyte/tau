package engine

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// pool.go is the serverless part: what is warm, how many run, how many
// requests one takes at a time, and when they go away.
//
// NONE OF IT IS ENGINE-SPECIFIC, which is why it is here rather than repeated
// in three implementations that would each get it subtly differently. An engine
// starts a thing and serves a request; everything below decides when.

// Defaults.
const (
	DefaultIdleTimeout  = 5 * time.Minute
	DefaultMaxInstances = 32
	DefaultTimeout      = 30 * time.Second
)

// Options configure a Pool.
type Options struct {
	// Engines are the tiers available. A spec naming one that is not here is
	// refused rather than defaulted: silently running a container spec on the
	// wasm engine would be a very confusing failure.
	Engines []Engine

	// IdleTimeout is how long an instance with no work is kept before it is
	// closed. Default 5m. This is scale-to-zero: when the last instance of a
	// spec ages out, that spec costs nothing until the next request, which
	// then pays a cold start.
	IdleTimeout time.Duration

	// MaxInstances is how many instances of ONE spec may exist at once.
	// Default 32.
	//
	// IT IS PER SPEC AND NOT GLOBAL, deliberately. A global cap makes one busy
	// function able to starve every other function on the node, which is the
	// noisy-neighbour failure that is hardest to diagnose because the victim's
	// own numbers all look fine.
	MaxInstances int

	// Timeout bounds one request when the Spec does not say. Default 30s.
	Timeout time.Duration

	// Now is the local clock. Tests inject one.
	Now func() time.Time
}

func (o Options) withDefaults() (Options, error) {
	if o.IdleTimeout == 0 {
		o.IdleTimeout = DefaultIdleTimeout
	}
	if o.MaxInstances == 0 {
		o.MaxInstances = DefaultMaxInstances
	}
	if o.Timeout == 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.IdleTimeout <= 0 || o.MaxInstances < 1 || o.Timeout <= 0 {
		return Options{}, fmt.Errorf("engine: idle %s, %d instances and timeout %s: all three need a positive budget",
			o.IdleTimeout, o.MaxInstances, o.Timeout)
	}
	return o, nil
}

// Pool keeps warm instances and routes requests to them.
type Pool struct {
	opts    Options
	engines map[string]Engine

	mu     sync.Mutex
	groups map[string]*group
	closed bool
}

// group is every instance of one spec — one revision of one thing.
type group struct {
	spec  Spec
	slots []*slot

	// sem bounds total in-flight requests for this group at
	// MaxInstances * Concurrency. Holding a token guarantees a slot is
	// available or can be created, by pigeonhole, so the code under the mutex
	// never has to wait.
	sem chan struct{}
}

type slot struct {
	inst     Instance
	inUse    int
	lastUsed time.Time
}

// NewPool builds a pool over the given engines.
func NewPool(opts Options) (*Pool, error) {
	o, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	p := &Pool{opts: o, engines: map[string]Engine{}, groups: map[string]*group{}}
	for _, e := range o.Engines {
		if e == nil {
			return nil, fmt.Errorf("engine: a nil engine was registered")
		}
		if _, dup := p.engines[e.Name()]; dup {
			return nil, fmt.Errorf("engine: %q registered twice", e.Name())
		}
		p.engines[e.Name()] = e
	}
	return p, nil
}

// Do runs one request against spec, starting an instance if nothing warm can
// take it, and waiting if the spec is at capacity.
func (p *Pool) Do(ctx context.Context, spec Spec, req *Request) (*Response, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	if spec.Concurrency == 0 {
		spec.Concurrency = 1
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}
	eng, ok := p.engines[spec.Engine]
	if !ok {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrNoEngine, spec.Engine)
	}
	g := p.groupFor(spec)
	sem := g.sem
	p.mu.Unlock()

	// Wait for room. THIS IS WHERE A BURST QUEUES rather than failing: a
	// serverless platform that returned 503 the moment every instance was busy
	// would be useless under exactly the traffic it exists for. The caller's
	// context is what bounds the wait, so a client that has given up stops
	// occupying a place in the queue.
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-sem }()

	s, err := p.acquire(ctx, eng, spec)
	if err != nil {
		return nil, err
	}

	timeout := spec.Timeout
	if timeout == 0 {
		timeout = p.opts.Timeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := s.inst.Serve(callCtx, req)
	p.release(spec, s, err != nil)
	return resp, err
}

// acquire finds a slot with room, or starts one.
//
// STARTING HAPPENS OUTSIDE THE LOCK. A cold start is the slowest thing this
// package does — seconds for a container — and holding the pool's mutex across
// it would make one cold start block every other spec on the node.
func (p *Pool) acquire(ctx context.Context, eng Engine, spec Spec) (*slot, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}
	g := p.groupFor(spec)
	for _, s := range g.slots {
		if s.inUse < spec.Concurrency {
			s.inUse++
			p.mu.Unlock()
			return s, nil
		}
	}
	if len(g.slots) >= p.opts.MaxInstances {
		// Cannot happen while the semaphore is sized MaxInstances*Concurrency,
		// and it is checked anyway: the alternative to this branch is starting
		// an unbounded number of instances if that sizing ever changes.
		p.mu.Unlock()
		return nil, ErrAtCapacity
	}
	p.mu.Unlock()

	inst, err := eng.Start(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("engine: starting %s/%s: %w", spec.Name, spec.Revision, err)
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		inst.Close()
		return nil, ErrPoolClosed
	}
	g = p.groupFor(spec)
	s := &slot{inst: inst, inUse: 1, lastUsed: p.opts.Now()}
	g.slots = append(g.slots, s)
	p.mu.Unlock()
	return s, nil
}

// release returns a slot, or discards it if the request failed.
//
// A FAILED REQUEST RETIRES THE INSTANCE. The pool cannot tell "this request was
// malformed" from "this process is now wedged", and the second is the one that
// costs: a broken instance kept warm serves every subsequent request badly,
// which looks like an intermittent fault rather than a dead worker. Throwing it
// away costs one cold start and is the cheap side of that trade.
func (p *Pool) release(spec Spec, s *slot, failed bool) {
	p.mu.Lock()
	s.inUse--
	s.lastUsed = p.opts.Now()

	if !failed {
		p.mu.Unlock()
		return
	}
	g, ok := p.groups[spec.key()]
	if ok {
		g.slots = removeSlot(g.slots, s)
		if len(g.slots) == 0 {
			delete(p.groups, spec.key())
		}
	}
	inUse := s.inUse
	p.mu.Unlock()

	// Another goroutine may still be inside Serve on this instance when
	// Concurrency > 1. Closing under it would turn one failure into several,
	// so the last one out closes.
	if inUse == 0 {
		s.inst.Close()
	}
}

// groupFor returns the group for spec, creating it. The caller holds the lock.
func (p *Pool) groupFor(spec Spec) *group {
	k := spec.key()
	if g, ok := p.groups[k]; ok {
		return g
	}
	g := &group{
		spec: spec,
		sem:  make(chan struct{}, p.opts.MaxInstances*spec.Concurrency),
	}
	p.groups[k] = g
	return g
}

// Reap closes instances that have been idle longer than IdleTimeout and returns
// how many it closed. This is scale-to-zero, and it is a method rather than a
// private ticker so that "an idle spec costs nothing" is a unit test with an
// injected clock instead of a sleep.
func (p *Pool) Reap() int {
	now := p.opts.Now()

	p.mu.Lock()
	var dead []Instance
	for k, g := range p.groups {
		kept := g.slots[:0]
		for _, s := range g.slots {
			if s.inUse == 0 && now.Sub(s.lastUsed) >= p.opts.IdleTimeout {
				dead = append(dead, s.inst)
				continue
			}
			kept = append(kept, s)
		}
		g.slots = kept
		if len(g.slots) == 0 {
			delete(p.groups, k)
		}
	}
	p.mu.Unlock()

	for _, inst := range dead {
		inst.Close()
	}
	return len(dead)
}

// StartReaper runs Reap on an interval until ctx ends.
func (p *Pool) StartReaper(ctx context.Context, every time.Duration) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.Reap()
			}
		}
	}()
}

// Warm reports how many instances of a spec are currently held.
func (p *Pool) Warm(spec Spec) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if g, ok := p.groups[spec.key()]; ok {
		return len(g.slots)
	}
	return 0
}

// Close shuts every instance down. It is safe to call twice.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	var dead []Instance
	for _, g := range p.groups {
		for _, s := range g.slots {
			dead = append(dead, s.inst)
		}
	}
	p.groups = map[string]*group{}
	p.mu.Unlock()

	for _, inst := range dead {
		inst.Close()
	}
	return nil
}

func removeSlot(slots []*slot, s *slot) []*slot {
	out := slots[:0]
	for _, cur := range slots {
		if cur != s {
			out = append(out, cur)
		}
	}
	return out
}
