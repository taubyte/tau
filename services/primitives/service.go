package primitives

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

var logger = log.Logger("tau.v2.primitives")

// Service is the consensus this node holds, the declarations it reads
// primitives out of, and the host it answers on.
type Service struct {
	host    host.Host
	declare *declarations

	// open is how a consensus group is opened. It is the composer's, because
	// raft.New takes a peer node — a host, a DHT, a datastore and a lifetime —
	// and none of that is this package's to assemble.
	open   func(namespace string) (raft.Cluster, error)
	stores kvdb.Factory

	records *records
	work    *work

	// crons is the clock. schedules, fire and task are the composer's; without schedules
	// and fire no clock runs. See cron.go.
	crons     *crons
	schedules func(context.Context) []Schedule
	fire      func(context.Context, Target, *invoke.Request) (*invoke.Response, error)
	task      func(context.Context, time.Time, time.Time) error

	// deliveries is every fire still being invoked, so Close waits for them. The loop is
	// waited out first (cycling): an Add racing the drained Wait panics.
	deliveries sync.WaitGroup

	// cycling is closed once the clock's loop has returned, so Close can stop
	// the only thing that adds to deliveries before it waits on them.
	cycling chan struct{}

	// working is this service's lifetime, cancelled only by Close. Fires run under it,
	// not the tasking request's context, bounded by their own claim deadlines.
	working context.Context

	now   func() time.Time
	sweep time.Duration
	dead  retention

	// forming is closed once the group has opened or failed, so "not yet" is told from
	// "not ever" without waiting. The group forms off the request path, since raft.New
	// blocks up to its 30s bootstrap timeout looking for peers.
	formMu  sync.Mutex
	forming chan struct{}
	cluster raft.Cluster
	formErr error

	serverOptions []rpc.ServerOption

	startMu   sync.Mutex
	server    *rpc.Server
	stopSweep context.CancelFunc
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

// Option injects what this service is built from.
type Option func(*options) error

type options struct {
	source register.Registry
	open   func(namespace string) (raft.Cluster, error)
	stores kvdb.Factory

	now   func() time.Time
	sweep time.Duration
	dead  retention

	schedules func(context.Context) []Schedule
	fire      func(context.Context, Target, *invoke.Request) (*invoke.Response, error)
	task      func(context.Context, time.Time, time.Time) error

	serverOptions []rpc.ServerOption
}

// WithRegister is where declarations are read from. Required, and it is the
// whole of why a caller cannot describe a queue — see declarations.go.
func WithRegister(source register.Registry) Option {
	return func(o *options) error {
		if source == nil {
			return ErrNoRegister
		}
		o.source = source
		return nil
	}
}

// WithConsensus is how this node opens the group its primitives run in.
// Required: both of this kind's types are decisions about who writes next, and
// a node that cannot hold an election can serve neither.
func WithConsensus(open func(namespace string) (raft.Cluster, error)) Option {
	return func(o *options) error {
		if open == nil {
			return ErrNoConsensus
		}
		o.open = open
		return nil
	}
}

// WithRecords is where a log's records live. Required: raft orders, the replicated
// store carries the bytes.
func WithRecords(f kvdb.Factory) Option {
	return func(o *options) error {
		if f == nil {
			return ErrNoRecords
		}
		o.stores = f
		return nil
	}
}

// WithSweep is how often this node runs the lifecycle pass. Zero means
// DefaultSweep.
func WithSweep(d time.Duration) Option {
	return func(o *options) error {
		if d < 0 {
			return fmt.Errorf("primitives: a sweep of %s", d)
		}
		o.sweep = d
		return nil
	}
}

// WithDeadLetterRetention bounds what a dead letter keeps. An operator setting, since
// the vocabulary has no author word for it yet; see DefaultDeadLetterRetention.
func WithDeadLetterRetention(age time.Duration, size int64) Option {
	return func(o *options) error {
		if age < 0 || size < 0 {
			return fmt.Errorf("primitives: a dead letter kept for %s or %d bytes", age, size)
		}
		o.dead = retention{age: age, size: size}
		return nil
	}
}

// WithTasking is how a window leaves the leader, injected because the composer
// already discovers primitives nodes and keeps streams to them. Without one the clock
// tasks itself.
func WithTasking(to func(ctx context.Context, since, upto time.Time) error) Option {
	return func(o *options) error {
		if to == nil {
			return errors.New("primitives: a nil tasking")
		}
		o.task = to
		return nil
	}
}

func WithClock(now func() time.Time) Option {
	return func(o *options) error {
		if now == nil {
			return errors.New("primitives: a nil clock")
		}
		o.now = now
		return nil
	}
}

// WithServerOptions passes p2p/rpc.ServerOption values through to rpc.Listen.
func WithServerOptions(opts ...rpc.ServerOption) Option {
	return func(o *options) error {
		o.serverOptions = append(o.serverOptions, opts...)
		return nil
	}
}

// New validates and allocates. It binds nothing and opens nothing — no
// listener until Start and no consensus group until then either — so a node can
// construct every service before starting any of them.
func New(h host.Host, opts ...Option) (*Service, error) {
	if h == nil {
		return nil, ErrNoHost
	}

	o := options{dead: DefaultDeadLetterRetention}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, fmt.Errorf("primitives: option: %w", err)
		}
	}
	switch {
	case o.source == nil:
		return nil, ErrNoRegister
	case o.open == nil:
		return nil, ErrNoConsensus
	case o.stores == nil:
		return nil, ErrNoRecords
	}

	// THE CLOCK'S TWO HALVES ARE ONE DECISION. A service told which schedules
	// exist and given no way to fire one would claim every slot in the cloud
	// and deliver nothing; one given a way to fire and no schedules would run a
	// pass over nothing. Either is a composition mistake worth refusing at
	// construction rather than discovering as a cron that stopped.
	if (o.schedules == nil) != (o.fire == nil) {
		return nil, ErrHalfAClock
	}

	projects, err := register.NewCache(o.source, specs.Load)
	if err != nil {
		return nil, err
	}

	sweep := o.sweep
	if sweep == 0 {
		sweep = DefaultSweep
	}
	now := o.now
	if now == nil {
		now = time.Now
	}

	s := &Service{
		host:          h,
		declare:       &declarations{projects: projects},
		open:          o.open,
		stores:        o.stores,
		now:           now,
		sweep:         sweep,
		dead:          o.dead,
		schedules:     o.schedules,
		fire:          o.fire,
		task:          o.task,
		serverOptions: o.serverOptions,
	}
	s.records = &records{service: s}
	s.work = &work{service: s}
	s.crons = &crons{service: s}

	// A CLOCK THAT WAS GIVEN NOWHERE TO SEND A WINDOW TASKS ITSELF, which is
	// the one node it is certain can be reached. It is the same call the wire
	// makes — see [Tier.CronTask] — so the local arm is the absence of a hop
	// and never a second path.
	if s.task == nil {
		s.task = s.Local().CronTask
	}
	return s, nil
}

// Start listens, begins forming the consensus group, and runs the lifecycle
// pass. Calling it twice, or after Close, is an error rather than a second
// listener leaking or a silent no-op.
func (s *Service) Start(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	if s.closed.Load() {
		return ErrClosed
	}
	if s.server != nil {
		return errors.New("primitives: already started")
	}

	server, err := rpc.Listen(ctx, s.host, Protocol, s.mount(), s.serverOptions...)
	if err != nil {
		return fmt.Errorf("primitives: listening: %w", err)
	}
	s.server = server
	s.form()

	// THE PASS RUNS FOR AS LONG AS THIS SERVICE DOES, not for as long as the
	// caller's context: a Start whose ctx ends when the caller returns would
	// leave a node that serves and never expires anything.
	sweepCtx, stopSweep := context.WithCancel(context.WithoutCancel(ctx))
	s.stopSweep = stopSweep
	s.working = sweepCtx

	cycling := make(chan struct{})
	s.cycling = cycling
	go func() {
		defer close(cycling)
		s.run(sweepCtx)
	}()

	return nil
}

// form opens the consensus group, once, in the background.
func (s *Service) form() {
	s.formMu.Lock()
	if s.forming != nil {
		s.formMu.Unlock()
		return
	}
	forming := make(chan struct{})
	s.forming = forming
	s.formMu.Unlock()

	go func() {
		defer close(forming)

		cluster, err := s.open(namespace)

		s.formMu.Lock()
		s.cluster, s.formErr = cluster, err
		if err != nil {
			s.formErr = fmt.Errorf("primitives: opening %s: %w", namespace, err)
		}
		s.formMu.Unlock()
	}()
}

// consensus is the group this node's primitives live in, or why there is not
// one yet. IT NEVER BLOCKS.
func (s *Service) consensus() (raft.Cluster, error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}

	s.formMu.Lock()
	forming, cluster, err := s.forming, s.cluster, s.formErr
	s.formMu.Unlock()

	if forming == nil {
		// Nothing has begun forming, which means this service was never
		// started. It is the same instruction as "not yet" and a different
		// fact; both are a caller that should be somewhere else.
		return nil, ErrForming
	}
	select {
	case <-forming:
	default:
		return nil, ErrForming
	}

	if err != nil {
		return nil, err
	}
	if cluster == nil {
		return nil, ErrClosed
	}
	return cluster, nil
}

// Formed is closed once the consensus group has opened or failed; nil on a service
// never started. The group forms off the request path, so a composer needs this to
// tell "not yet" from "not ever".
func (s *Service) Formed() <-chan struct{} {
	s.formMu.Lock()
	defer s.formMu.Unlock()
	return s.forming
}

// writable refuses a write on a node that is not the one that may make it, with
// the leader's name on the refusal.
func (s *Service) writable(cluster raft.Cluster) error {
	if cluster.IsLeader() {
		return nil
	}
	return s.notLeading(cluster)
}

// notLeading is the refusal with whoever does lead named on it.
//
// A LEADER THAT CANNOT BE READ IS AN UNNAMED ONE AND NOT AN ERROR OF ITS OWN.
// The fact the caller acts on is that this node may not write; which node may
// is the part that helps, and its absence must not turn a retryable refusal
// into something that reads like a broken cluster.
func (s *Service) notLeading(cluster raft.Cluster) error {
	leader, err := cluster.Leader()
	if err != nil {
		return NotLeading{}
	}
	return NotLeading{Leader: leader.String()}
}

// declared resolves one resource to what its author wrote, refusing what this
// service cannot serve before what the caller asked for.
//
// want is the type the CALL implies — `queue` for a send, `log` for an append —
// and empty for a call that works on either.
func (s *Service) declared(ctx context.Context, at address, want string) (declared, error) {
	d, err := s.declare.of(ctx, at)
	if err != nil {
		return declared{}, err
	}
	if err := d.serves(want); err != nil {
		return declared{}, err
	}
	return d, nil
}

// The six operations below are the whole of what a call does, and both arms (compute
// tier and wire handler) call them. p2p/rpc's short-circuit makes the arm invisible
// to callers, so the arms must share the implementation, not merely agree.

func (s *Service) send(ctx context.Context, at address, id string, data []byte) error {
	d, err := s.declared(ctx, at, queueType)
	if err != nil {
		return err
	}
	return s.work.send(d, id, data)
}

// claim hands items out exclusively. An empty answer is not a refusal: the
// queue may still hold items that are delayed, backing off, or claimed by
// somebody else.
func (s *Service) claim(ctx context.Context, at address, max int) ([]Delivery, error) {
	d, err := s.declared(ctx, at, queueType)
	if err != nil {
		return nil, err
	}
	return s.work.claim(d, max)
}

func (s *Service) settle(ctx context.Context, at address, delivery Delivery, done bool) error {
	d, err := s.declared(ctx, at, queueType)
	if err != nil {
		return err
	}
	return s.work.settle(d, delivery, done)
}

func (s *Service) depth(ctx context.Context, at address) (Depth, error) {
	d, err := s.declared(ctx, at, queueType)
	if err != nil {
		return Depth{}, err
	}
	return s.work.depth(d)
}

func (s *Service) appendRecord(ctx context.Context, at address, data []byte) (string, error) {
	d, err := s.declared(ctx, at, logType)
	if err != nil {
		return "", err
	}
	return s.records.append(ctx, d.spec, data)
}

func (s *Service) readRecords(ctx context.Context, at address, after string, limit uint32) ([]wasmtier.Record, error) {
	d, err := s.declared(ctx, at, logType)
	if err != nil {
		return nil, err
	}
	return s.records.read(ctx, d.spec, after, limit)
}

func (s *Service) clock() time.Time { return s.now() }

// lifetime is how long work this service started on its own behalf may run.
//
// A SERVICE THAT WAS NEVER STARTED HAS NO LIFETIME AND NOT A DEAD ONE. Nothing
// reaches this before Start — [Service.consensus] refuses first — and a
// cancelled context here would be a fire that ends before it begins rather than
// a call that is told to come back.
func (s *Service) lifetime() context.Context {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.working == nil {
		return context.Background()
	}
	return s.working
}

// Close stops serving, ends the lifecycle pass and releases the group.
//
// IT WAITS FOR THE GROUP TO FINISH FORMING. Tearing one down mid-bootstrap
// leaves the goroutine writing into a closed store. The queues themselves hold
// no state — everything is in the store, which is what lets a process die
// mid-claim and the claim expire.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)

		s.startMu.Lock()
		server := s.server
		s.server = nil
		stop := s.stopSweep
		s.stopSweep = nil
		cycling := s.cycling
		s.cycling = nil
		s.startMu.Unlock()

		if stop != nil {
			stop()
		}

		// THE LOOP GOES BEFORE THE FIRES IT HANDS OUT. It is the only thing
		// that adds to the WaitGroup below, and a turn already inside a cycle
		// when the cancel landed goes on to claim one more — an Add racing the
		// Wait that drained it, which the runtime answers with a panic rather
		// than a leaked goroutine.
		if cycling != nil {
			<-cycling
		}

		// THE FIRES IN FLIGHT ARE WAITED OUT, for the reason the forming group
		// below is: a goroutine still settling a claim while the group is torn
		// down is a write into a store that is going away.
		s.deliveries.Wait()

		if server != nil {
			s.closeErr = server.Close()
		}

		s.formMu.Lock()
		forming := s.forming
		s.formMu.Unlock()
		if forming != nil {
			<-forming
		}

		s.formMu.Lock()
		cluster := s.cluster
		s.cluster = nil
		s.formMu.Unlock()

		s.closeErr = errors.Join(s.closeErr, s.records.Close())
		if cluster != nil {
			s.closeErr = errors.Join(s.closeErr, cluster.Close())
		}
	})
	return s.closeErr
}
