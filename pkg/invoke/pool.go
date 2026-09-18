package invoke

import (
	"context"
	"errors"
	"fmt"
	"net"
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

// Option configures a Pool. Everything optional is a With…; the only thing a
// pool cannot be built without is at least one runtime, and that is an error
// rather than a positional argument because a caller composing a node registers
// tiers one at a time as it discovers which are available.
type Option func(*options) error

type options struct {
	runtimes     []Runtime
	idleTimeout  time.Duration
	maxInstances int
	timeout      time.Duration
	now          func() time.Time
}

// WithRuntime registers a tier. Call it once per tier; a spec naming one that
// was never registered is refused rather than defaulted, because silently
// running a container spec on the wasm tier would be a very confusing failure.
func WithRuntime(rts ...Runtime) Option {
	return func(o *options) error {
		for _, rt := range rts {
			if rt == nil {
				return errors.New("invoke: a nil runtime was registered")
			}
			o.runtimes = append(o.runtimes, rt)
		}
		return nil
	}
}

// WithIdleTimeout is how long an instance with no work is kept before it is
// closed. Default 5m.
//
// THIS IS SCALE-TO-ZERO: when the last instance of a spec ages out, that spec
// costs nothing until the next request, which then pays a cold start.
func WithIdleTimeout(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("invoke: idle timeout %s: an instance closed the moment it is free is never warm", d)
		}
		o.idleTimeout = d
		return nil
	}
}

// WithMaxInstances is how many instances of ONE spec may exist at once.
// Default 32.
//
// IT IS PER SPEC AND NOT GLOBAL, deliberately. A global cap lets one busy
// function starve every other function on the node, which is the
// noisy-neighbour failure that is hardest to diagnose because the victim's own
// numbers all look fine.
func WithMaxInstances(n int) Option {
	return func(o *options) error {
		if n < 1 {
			return fmt.Errorf("invoke: %d instances: a spec that may not run is not a spec", n)
		}
		o.maxInstances = n
		return nil
	}
}

// WithTimeout bounds one request when the Spec does not say. Default 30s.
func WithTimeout(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("invoke: timeout %s: every request would be over before it began", d)
		}
		o.timeout = d
		return nil
	}
}

// WithClock injects the local clock. Tests use it; production does not.
func WithClock(now func() time.Time) Option {
	return func(o *options) error {
		if now == nil {
			return errors.New("invoke: a nil clock")
		}
		o.now = now
		return nil
	}
}

func (o *options) withDefaults() error {
	if o.idleTimeout == 0 {
		o.idleTimeout = DefaultIdleTimeout
	}
	if o.maxInstances == 0 {
		o.maxInstances = DefaultMaxInstances
	}
	if o.timeout == 0 {
		o.timeout = DefaultTimeout
	}
	if o.now == nil {
		o.now = time.Now
	}
	if len(o.runtimes) == 0 {
		return ErrNoRuntimes
	}
	return nil
}

// Pool keeps warm instances and routes requests to them.
type Pool struct {
	opts     options
	runtimes map[string]Runtime

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

	// starting is closed when the cold start currently in flight finishes, and
	// is nil when none is.
	//
	// WITHOUT IT A BURST ON A COLD SPEC STARTS AN INSTANCE PER REQUEST. Every
	// arrival finds no warm slot, and every one of them starts its own — so a
	// spec with concurrency 4 that could have served the whole burst from one
	// instance instead starts MaxInstances of them. With a wasm module that is
	// wasteful; with a container it is seconds of latency and hundreds of
	// megabytes each, for a burst that one instance would have absorbed.
	//
	// So the first arrival starts, and the rest WAIT for it and then look
	// again. They only start their own if the instance that appears is already
	// full to its concurrency.
	starting chan struct{}
}

type slot struct {
	inst     Instance
	inUse    int
	lastUsed time.Time
}

// NewPool builds a pool over the registered runtimes.
func NewPool(opts ...Option) (*Pool, error) {
	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, err
		}
	}
	if err := o.withDefaults(); err != nil {
		return nil, err
	}

	p := &Pool{opts: o, runtimes: map[string]Runtime{}, groups: map[string]*group{}}
	for _, rt := range o.runtimes {
		if _, dup := p.runtimes[rt.Name()]; dup {
			return nil, fmt.Errorf("invoke: %q registered twice", rt.Name())
		}
		p.runtimes[rt.Name()] = rt
	}
	return p, nil
}

// Do runs one request against spec, starting an instance if nothing warm can
// take it, and waiting if the spec is at capacity.
func (p *Pool) Do(ctx context.Context, spec Spec, req *Request) (*Response, error) {
	var out *Response
	err := p.on(ctx, spec, func(ctx context.Context, inst Instance) error {
		var err error
		out, err = inst.Serve(ctx, req)
		return err
	})
	return out, err
}

// Transform runs one request through spec as a TRANSFORMER, on a pooled
// instance, and answers with what it decided.
//
// IT IS POOLED FOR THE SAME REASON A HANDLER IS, and that is the whole argument
// for it being here rather than in the tier. An authorizer or an instance
// validator is asked once per caller and its answer is kept, so each can afford
// to instantiate per question; a transformer runs on EVERY request and nothing
// about its answer can be kept. Everything this pool does — warm slots,
// cold-start deduplication, per-spec concurrency, idle timeout, scale to zero —
// is about when to start and reuse a thing, not about what the thing answers.
//
// A TIER THAT CANNOT TRANSFORM SAYS SO. Only the component tier serves this
// world; a container is a process that answers requests and has no second
// export to call.
func (p *Pool) Transform(ctx context.Context, spec Spec, req *Request) (*Outcome, error) {
	var out *Outcome
	err := p.on(ctx, spec, func(ctx context.Context, inst Instance) error {
		t, ok := inst.(Transforms)
		if !ok {
			return fmt.Errorf("%w: the %s tier runs no transformer", ErrNoRuntime, spec.Runtime)
		}
		var err error
		out, err = t.Transform(ctx, req)
		return err
	})
	return out, err
}

// Route asks spec, as a ROUTER, where one request should go.
//
// POOLED FOR THE SAME REASON Transform IS: a router decides per request and
// nothing about its answer can be kept.
func (p *Pool) Route(ctx context.Context, spec Spec, req *Request) (*Routing, error) {
	var out *Routing
	err := p.on(ctx, spec, func(ctx context.Context, inst Instance) error {
		r, ok := inst.(Routes)
		if !ok {
			return fmt.Errorf("%w: the %s tier runs no router", ErrNoRuntime, spec.Runtime)
		}
		var err error
		out, err = r.Route(ctx, req)
		return err
	})
	return out, err
}

// Conn opens a connection to one of spec's ports and holds the instance for as
// long as the connection lives.
//
// IT IS THE ONE CALL HERE THAT OUTLIVES ITSELF. Do and Transform borrow a slot
// for the duration of a call and give it back; a connection is held by whoever
// dialled it, for as long as they like, so the slot goes back when the
// connection closes and not before. That is what makes the instance's
// concurrency ceiling mean the same thing for a socket as it does for a
// request, and what stops the idle reaper closing a container somebody is
// talking to.
//
// THE SEMAPHORE IS HELD TOO, which is the part worth saying out loud: a spec
// whose connections are all long-lived reaches its ceiling and then queues,
// exactly as a spec under sustained request load does. A proxy that wanted more
// than that would be asking for a bigger ceiling, which is a number its author
// writes.
func (p *Pool) Conn(ctx context.Context, spec Spec, port string) (net.Conn, error) {
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
	rt, ok := p.runtimes[spec.Runtime]
	if !ok {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrNoRuntime, spec.Runtime)
	}
	g := p.groupFor(spec)
	sem := g.sem
	p.mu.Unlock()

	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	s, err := p.acquire(ctx, rt, spec)
	if err != nil {
		<-sem
		return nil, err
	}
	release := func(failed bool) { p.release(spec, s, failed) }

	dials, ok := s.inst.(Dials)
	if !ok {
		release(false)
		<-sem
		return nil, fmt.Errorf("%w: the %s tier hands out no connection", ErrNoRuntime, spec.Runtime)
	}

	// NO CALL TIMEOUT HERE, unlike Do. A connection has no duration this
	// package could know: a database session is measured in hours and a health
	// probe in milliseconds, and cutting either at a default would be this
	// package inventing a policy. What bounds the DIAL is the caller's context.
	conn, err := dials.Dial(ctx, port)
	if err != nil {
		release(true)
		<-sem
		return nil, err
	}

	return &pooled{Conn: conn, release: release, sem: sem}, nil
}

// pooled is a connection that gives its instance back when it is closed.
type pooled struct {
	net.Conn
	release func(failed bool)
	once    sync.Once
	sem     chan struct{}
}

// CloseWrite forwards a half-close to the connection underneath.
//
// IT HAS TO BE WRITTEN OUT because this type embeds the net.Conn INTERFACE, and
// promotion through an interface gives only that interface's methods. A caller
// type-asserting for CloseWrite on a pooled connection would find nothing and
// fall back to closing the whole thing — which ends the half a query protocol is
// waiting on, so the client's request is delivered and the answer never comes.
//
// It reports whether the connection underneath could do it, rather than
// swallowing the answer: a caller that cannot half-close needs to know, because
// its alternative is a full close and pretending otherwise leaves the far end
// waiting for an EOF nobody sent.
func (c *pooled) CloseWrite() error {
	cw, ok := c.Conn.(interface{ CloseWrite() error })
	if !ok {
		return fmt.Errorf("%w: this connection cannot be half-closed", ErrNoRuntime)
	}
	return cw.CloseWrite()
}

func (c *pooled) Close() error {
	err := c.Conn.Close()
	// ONCE, because Close on a net.Conn is routinely called twice — a defer and
	// an explicit one — and releasing twice would hand the slot out to two
	// callers at the same ceiling.
	c.once.Do(func() {
		c.release(false)
		<-c.sem
	})
	return err
}

// on runs fn on a pooled instance of spec — the whole of what this package
// decides, with what to CALL left to the caller.
func (p *Pool) on(ctx context.Context, spec Spec, fn func(context.Context, Instance) error) error {
	if err := spec.validate(); err != nil {
		return err
	}
	if spec.Concurrency == 0 {
		spec.Concurrency = 1
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPoolClosed
	}
	rt, ok := p.runtimes[spec.Runtime]
	if !ok {
		p.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrNoRuntime, spec.Runtime)
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
		return ctx.Err()
	}
	defer func() { <-sem }()

	s, err := p.acquire(ctx, rt, spec)
	if err != nil {
		return err
	}

	timeout := spec.Timeout
	if timeout == 0 {
		timeout = p.opts.timeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err = fn(callCtx, s.inst)
	p.release(spec, s, err != nil)
	return err
}

// acquire finds a slot with room, or starts one.
//
// STARTING HAPPENS OUTSIDE THE LOCK. A cold start is the slowest thing this
// package does — seconds for a container — and holding the pool's mutex across
// it would make one cold start block every other spec on the node.
func (p *Pool) acquire(ctx context.Context, rt Runtime, spec Spec) (*slot, error) {
	for {
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
		if len(g.slots) >= p.opts.maxInstances {
			// Cannot happen while the semaphore is sized
			// MaxInstances*Concurrency, and it is checked anyway: the
			// alternative to this branch is starting an unbounded number of
			// instances if that sizing ever changes.
			p.mu.Unlock()
			return nil, ErrAtCapacity
		}

		// Somebody else is already starting one. Wait for it and look again
		// rather than starting a second — see group.starting.
		if g.starting != nil {
			wait := g.starting
			p.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		started := make(chan struct{})
		g.starting = started
		p.mu.Unlock()

		// STARTING HAPPENS OUTSIDE THE LOCK. A cold start is the slowest thing
		// this package does — seconds for a container — and holding the pool's
		// mutex across it would make one cold start block every other spec on
		// the node.
		inst, err := rt.Start(ctx, spec)

		p.mu.Lock()
		g = p.groupFor(spec)
		g.starting = nil
		close(started) // whatever happened, the waiters must look again

		if err != nil {
			p.mu.Unlock()
			return nil, fmt.Errorf("engine: starting %s/%s: %w", spec.Name, spec.Revision, err)
		}
		if p.closed {
			p.mu.Unlock()
			inst.Close()
			return nil, ErrPoolClosed
		}
		s := &slot{inst: inst, inUse: 1, lastUsed: p.opts.now()}
		g.slots = append(g.slots, s)
		p.mu.Unlock()
		return s, nil
	}
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
	s.lastUsed = p.opts.now()

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
		sem:  make(chan struct{}, p.opts.maxInstances*spec.Concurrency),
	}
	p.groups[k] = g
	return g
}

// Reap closes instances that have been idle longer than IdleTimeout and returns
// how many it closed. This is scale-to-zero, and it is a method rather than a
// private ticker so that "an idle spec costs nothing" is a unit test with an
// injected clock instead of a sleep.
func (p *Pool) Reap() int {
	now := p.opts.now()

	p.mu.Lock()
	var dead []Instance
	for k, g := range p.groups {
		kept := g.slots[:0]
		for _, s := range g.slots {
			if s.inUse == 0 && now.Sub(s.lastUsed) >= p.opts.idleTimeout {
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
