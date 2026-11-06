package invoke

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// pool.go is the serverless part: what is warm, how many run, how many requests one
// takes, and when they go away; none of it engine-specific.

// Defaults.
const (
	DefaultIdleTimeout  = 5 * time.Minute
	DefaultMaxInstances = 32
	DefaultTimeout      = 30 * time.Second
)

// Option configures a Pool; at least one runtime is required.
type Option func(*options) error

type options struct {
	runtimes     []Runtime
	idleTimeout  time.Duration
	maxInstances int
	timeout      time.Duration
	now          func() time.Time
}

// WithRuntime registers a tier; a spec naming an unregistered one is refused, never
// defaulted.
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

// WithIdleTimeout is how long an idle instance is kept; default 5m. When a spec's
// last instance ages out it costs nothing until the next request's cold start.
func WithIdleTimeout(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("invoke: idle timeout %s: an instance closed the moment it is free is never warm", d)
		}
		o.idleTimeout = d
		return nil
	}
}

// WithMaxInstances is how many instances of one spec may exist; default 32. Per
// spec, so one busy function cannot starve the rest of the node.
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
//
// A call takes no pool-wide lock. runtimes is never written after NewPool, closed is
// atomic, and groups is a specKey's *group; each group locks its own slots, so two
// specs never contend. mu orders Close against a Reap.
type Pool struct {
	opts     options
	runtimes map[string]Runtime

	mu     sync.Mutex
	groups sync.Map
	closed atomic.Bool
}

// group is every instance of one spec — one revision of one thing. mu guards slots,
// starting and gone, and each slot's inUse and lastUsed.
type group struct {
	spec  Spec
	mu    sync.Mutex
	slots []*slot

	// gone is set when the group leaves the pool. A caller still holding it looks its
	// spec up again, so no instance is added where Reap and Close cannot reach it.
	gone bool

	// sem bounds total in-flight requests for this group at
	// MaxInstances * Concurrency. Holding a token guarantees a slot is
	// available or can be created, by pigeonhole, so the code under the mutex
	// never has to wait.
	sem *gate

	// starting is closed when the in-flight cold start finishes, nil when none is.
	// Without it a burst on a cold spec would start one instance per request; waiters
	// look again and start their own only if the new instance is already full.
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

	p := &Pool{opts: o, runtimes: map[string]Runtime{}}
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

// Transform runs one request through spec as a transformer on a pooled instance.
// Pooled because a transformer runs on every request and its answer cannot be
// kept. A tier that cannot transform says so.
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

// Route asks spec, as a router, where one request should go; pooled like
// Transform.
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

// Direct asks spec, as a proxy router, where one connection should go. Each call
// hands over the whole prefix, so no instance remembers a connection.
func (p *Pool) Direct(ctx context.Context, spec Spec, conn *Connection, peeked []byte) (*Direction, error) {
	var out *Direction
	err := p.on(ctx, spec, func(ctx context.Context, inst Instance) error {
		d, ok := inst.(Directs)
		if !ok {
			return fmt.Errorf("%w: the %s tier runs no proxy router", ErrNoRuntime, spec.Runtime)
		}
		var err error
		out, err = d.Direct(ctx, conn, peeked)
		return err
	})
	return out, err
}

// Shape runs one chunk of a connection through spec as a proxy transformer; pooled
// because its state travels with the call.
func (p *Pool) Shape(ctx context.Context, spec Spec, s *Stream) (*Shaping, error) {
	var out *Shaping
	err := p.on(ctx, spec, func(ctx context.Context, inst Instance) error {
		sh, ok := inst.(Shapes)
		if !ok {
			return fmt.Errorf("%w: the %s tier runs no proxy transformer", ErrNoRuntime, spec.Runtime)
		}
		var err error
		out, err = sh.Shape(ctx, s)
		return err
	})
	return out, err
}

// Conn opens a connection to one of spec's ports and holds the instance and its
// semaphore token until the connection closes, so the concurrency ceiling means
// the same for a socket as for a request and the reaper never closes a container
// in use.
func (p *Pool) Conn(ctx context.Context, spec Spec, port string) (net.Conn, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	if spec.Concurrency == 0 {
		spec.Concurrency = 1
	}

	if p.closed.Load() {
		return nil, ErrPoolClosed
	}
	rt, ok := p.runtimes[spec.Runtime]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNoRuntime, spec.Runtime)
	}
	g := p.groupFor(spec)
	sem := g.sem
	if err := sem.enter(ctx); err != nil {
		return nil, err
	}

	s, g, err := p.acquire(ctx, rt, g)
	if err != nil {
		sem.leave()
		return nil, err
	}
	release := func(failed bool) { p.release(g, s, failed) }

	dials, ok := s.inst.(Dials)
	if !ok {
		release(false)
		sem.leave()
		return nil, fmt.Errorf("%w: the %s tier hands out no connection", ErrNoRuntime, spec.Runtime)
	}

	// No call timeout: a connection's duration is the caller's; the caller's context
	// bounds the dial.
	conn, err := dials.Dial(ctx, port)
	if err != nil {
		release(true)
		sem.leave()
		return nil, err
	}

	return &pooled{Conn: conn, release: release, sem: sem}, nil
}

// pooled is a connection that gives its instance back when it is closed.
type pooled struct {
	net.Conn
	release func(failed bool)
	once    sync.Once
	sem     *gate
}

// CloseWrite forwards a half-close, written out because embedding the net.Conn
// interface does not promote it; a caller falling back to a full close would lose
// the answer. It reports whether the connection could.
func (c *pooled) CloseWrite() error {
	cw, ok := c.Conn.(interface{ CloseWrite() error })
	if !ok {
		return fmt.Errorf("%w: this connection cannot be half-closed", ErrNoRuntime)
	}
	return cw.CloseWrite()
}

func (c *pooled) Close() error {
	err := c.Conn.Close()
	// Once: Close is often called twice, and releasing twice would double the ceiling.
	c.once.Do(func() {
		c.release(false)
		c.sem.leave()
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

	if p.closed.Load() {
		return ErrPoolClosed
	}
	rt, ok := p.runtimes[spec.Runtime]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNoRuntime, spec.Runtime)
	}
	g := p.groupFor(spec)
	sem := g.sem

	// Wait for room: a burst queues rather than failing, bounded by the caller's
	// context.
	if err := sem.enter(ctx); err != nil {
		return err
	}

	s, g, err := p.acquire(ctx, rt, g)
	if err != nil {
		sem.leave()
		return err
	}

	timeout := spec.Timeout
	if timeout == 0 {
		timeout = p.opts.timeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	c := &calling{Context: callCtx}
	err = fn(c, s.inst)
	cancel()

	if err == nil && c.l.claimed.Load() {
		go p.settle(g, s, sem, &c.l)
		return nil
	}
	p.release(g, s, err != nil)
	sem.leave()
	return err
}

// settle releases a lingering call's slot once the work it kept going is done.
func (p *Pool) settle(g *group, s *slot, sem *gate, l *linger) {
	p.release(g, s, <-l.settled)
	sem.leave()
}

// Linger keeps the instance serving ctx's call checked out after it answered, until
// done is called (failed retires it), for work like `ctx.waitUntil`. It must be
// claimed before the call returns; ok is false outside a pooled call.
func Linger(ctx context.Context) (done func(failed bool), ok bool) {
	l, ok := ctx.Value(lingerKey{}).(*linger)
	if !ok {
		return nil, false
	}
	l.claim.Do(func() { l.settled = make(chan bool, 1) })
	l.claimed.Store(true)
	return l.done, true
}

type lingerKey struct{}

// calling is a pooled call's context, carrying its linger: one allocation for both.
type calling struct {
	context.Context
	l linger
}

func (c *calling) Value(key any) any {
	if key == (lingerKey{}) {
		return &c.l
	}
	return c.Context.Value(key)
}

// settled is made at the claim, so a call that never lingers allocates no channel.
type linger struct {
	claim   sync.Once
	claimed atomic.Bool
	once    sync.Once
	settled chan bool
}

func (l *linger) done(failed bool) { l.once.Do(func() { l.settled <- failed }) }

// acquire finds a slot in g with room or starts one, outside the lock so one cold
// start does not block other callers. It answers the group the slot is in: g's spec
// again if g left the pool meanwhile.
func (p *Pool) acquire(ctx context.Context, rt Runtime, g *group) (*slot, *group, error) {
	spec := g.spec
	for {
		g.mu.Lock()
		if g.gone {
			g.mu.Unlock()
			g = p.groupFor(spec)
			continue
		}
		if p.closed.Load() {
			g.mu.Unlock()
			return nil, nil, ErrPoolClosed
		}
		for _, s := range g.slots {
			if s.inUse < spec.Concurrency {
				s.inUse++
				g.mu.Unlock()
				return s, g, nil
			}
		}
		if len(g.slots) >= p.opts.maxInstances {
			// Cannot happen while the semaphore is sized
			// MaxInstances*Concurrency, and it is checked anyway: the
			// alternative to this branch is starting an unbounded number of
			// instances if that sizing ever changes.
			g.mu.Unlock()
			return nil, nil, ErrAtCapacity
		}

		// Somebody else is already starting one. Wait for it and look again
		// rather than starting a second — see group.starting.
		if g.starting != nil {
			wait := g.starting
			g.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}

		started := make(chan struct{})
		g.starting = started
		g.mu.Unlock()

		// Outside the lock. A group starting an instance is never removed, so the
		// slot below lands in a group the pool still holds.
		inst, err := rt.Start(ctx, spec)

		g.mu.Lock()
		g.starting = nil
		close(started) // whatever happened, the waiters must look again

		if err != nil {
			g.mu.Unlock()
			return nil, nil, fmt.Errorf("engine: starting %s/%s: %w", spec.Name, spec.Revision, err)
		}
		if p.closed.Load() {
			g.mu.Unlock()
			inst.Close()
			return nil, nil, ErrPoolClosed
		}
		s := &slot{inst: inst, inUse: 1, lastUsed: p.opts.now()}
		g.slots = append(g.slots, s)
		g.mu.Unlock()
		return s, g, nil
	}
}

// release returns a slot, or retires the instance if the request failed: a
// malformed request and a wedged process look alike, and a cold start is cheaper
// than guessing which.
func (p *Pool) release(g *group, s *slot, failed bool) {
	g.mu.Lock()
	s.inUse--
	s.lastUsed = p.opts.now()

	if !failed {
		g.mu.Unlock()
		return
	}
	g.slots = removeSlot(g.slots, s)
	p.leaveIfEmpty(g)
	inUse := s.inUse
	g.mu.Unlock()

	// With Concurrency > 1 others may still be in Serve, so the last one out closes.
	if inUse == 0 {
		s.inst.Close()
	}
}

// leaveIfEmpty takes g out of the pool when it holds no instance and is starting
// none. It is called with g.mu held.
func (p *Pool) leaveIfEmpty(g *group) {
	if len(g.slots) == 0 && g.starting == nil && !g.gone {
		g.gone = true
		p.groups.CompareAndDelete(g.spec.key(), g)
	}
}

// groupFor is spec's group, made on its first call.
func (p *Pool) groupFor(spec Spec) *group {
	k := spec.key()
	if g, ok := p.groups.Load(k); ok {
		return g.(*group)
	}
	g, _ := p.groups.LoadOrStore(k, &group{
		spec: spec,
		sem:  newGate(p.opts.maxInstances * spec.Concurrency),
	})
	return g.(*group)
}

// Reap closes instances idle longer than IdleTimeout and returns how many, a method
// so scale-to-zero is testable with an injected clock.
func (p *Pool) Reap() int {
	now := p.opts.now()

	p.mu.Lock()
	var dead []Instance
	p.groups.Range(func(_, held any) bool {
		g := held.(*group)
		g.mu.Lock()
		kept := g.slots[:0]
		for _, s := range g.slots {
			if s.inUse == 0 && now.Sub(s.lastUsed) >= p.opts.idleTimeout {
				dead = append(dead, s.inst)
				continue
			}
			kept = append(kept, s)
		}
		g.slots = kept
		p.leaveIfEmpty(g)
		g.mu.Unlock()
		return true
	})
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
	held, ok := p.groups.Load(spec.key())
	if !ok {
		return 0
	}
	g := held.(*group)
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.slots)
}

// Close shuts every instance down. It is safe to call twice.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed.Swap(true) {
		p.mu.Unlock()
		return nil
	}
	var dead []Instance
	p.groups.Range(func(k, held any) bool {
		g := held.(*group)
		g.mu.Lock()
		for _, s := range g.slots {
			dead = append(dead, s.inst)
		}
		g.slots, g.gone = nil, true
		g.mu.Unlock()
		p.groups.Delete(k)
		return true
	})
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
