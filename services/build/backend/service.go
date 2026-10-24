package backend

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/services/build"
)

// Service is build's private half: the host it answers on, the replicated
// store it was handed, the queue over that store, and what it was given to run
// work with.
type Service struct {
	host  host.Host
	store Store
	repos Repositories
	cfg   Config

	confirm Confirmer

	group *queue.Group
	work  *queue.Queue
	dead  *queue.Queue

	runner Runner
	// assetStore is the ONE DOOR IN THIS PACKAGE THAT LEAVES THIS MACHINE, and
	// it is named for the door rather than for what goes through it: the record
	// has an Assets map and a run has the cids it collected, so a field spelled
	// `assets` was one identifier standing for three things and a census keyed
	// on it could not ask about any of them (population_test.go).
	assetStore Assets
	authz      Authorizer
	now        func() time.Time

	serverOptions []rpc.ServerOption

	// startMu guards server and the loop's lifetime: Start and Close both
	// read-then-write them, and nothing about this type promises a caller will
	// only ever touch one Service from one goroutine.
	startMu sync.Mutex
	server  *rpc.Server
	stop    context.CancelCauseFunc
	stopped chan struct{}

	// running holds the cancel of every job THIS node is currently running, so
	// Cancel can stop one in the same process that wrote the record. It is the
	// whole of what v1 needed a monkey p2p round trip for.
	runningMu sync.Mutex
	running   map[string]context.CancelFunc

	// logs holds the log of every build this node is WRITING ONE FOR, with the
	// run it belongs to, so that Follow can fork a reader off it while it is
	// still being appended to — and goes on holding it until the run's record
	// names it (output.go's discard).
	//
	// IT IS A SECOND MAP AND NOT A SECOND OPINION about what this node runs.
	// The one above is decided by a record READ — the claim's, inside its
	// section, which is what makes a cancel find something to stop — and this
	// one is decided by the run itself having started, like Service.forget and
	// for the same reason: a build holds no lock, and opening its log is not a
	// transition of anything.
	logsMu sync.Mutex
	logs   map[string]runLog

	// meter is where this service says what it accounted for. Nil is the
	// ordinary case; see meter.go.
	meter Meter

	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

// Option configures a Service at construction. v2's tunables are fields set
// through Option, never package-level vars every instance in a process would
// share.
// Option configures an options value, not the Service. A Service is built once,
// from a validated snapshot, so there is no window in which a half-configured
// one exists and no second way to make one.
type Option func(*options) error

type options struct {
	host  host.Host
	store Store
	repos Repositories

	runner        Runner
	confirm       Confirmer
	assetStore    Assets
	meter         Meter
	authz         Authorizer
	now           func() time.Time
	serverOptions []rpc.ServerOption
}

// WithHost is the libp2p host this service answers on. Required.
//
// A NIL HOST IS AN ERROR RATHER THAN A NODE THIS SERVICE INVENTS. v1 has that
// fallback copied across four services, and a service that can invent its own
// libp2p identity can be started a way production never starts it.
func WithHost(h host.Host) Option {
	return func(o *options) error {
		if h == nil {
			return errors.New("build: no host to answer on")
		}
		o.host = h
		return nil
	}
}

// WithStore is where the queue and the job records persist. Required.
func WithStore(st Store) Option {
	return func(o *options) error {
		if st == nil {
			return errors.New("build: no store")
		}
		o.store = st
		return nil
	}
}

// WithRepositories resolves a project. Required.
func WithRepositories(r Repositories) Option {
	return func(o *options) error {
		if r == nil {
			return errors.New("build: no repositories to resolve a project with")
		}
		o.repos = r
		return nil
	}
}

// WithConfirmer is what a push is checked against before it is queued: the
// repository itself, asked whether the ref is at the commit. Without it a push
// is queued as the frontend verified it.
func WithConfirmer(c Confirmer) Option {
	return func(o *options) error {
		if c == nil {
			return errors.New("build: confirmer must not be nil")
		}
		o.confirm = c
		return nil
	}
}

// WithServerOptions passes p2p/rpc.ServerOption values through to rpc.Listen
// in Start without this package needing an option of its own for each one.
func WithServerOptions(opts ...rpc.ServerOption) Option {
	return func(o *options) error {
		o.serverOptions = append(o.serverOptions, opts...)
		return nil
	}
}

// WithRunner gives this node something to run claimed work with. WITHOUT ONE
// THE NODE CLAIMS NOTHING: it still serves the RPCs, still replicates, and
// still keeps the queue swept — a node can be a voter in the consistency
// domain without being a builder, and that is a deployment's choice rather
// than a degraded state.
func WithRunner(r Runner) Option {
	return func(o *options) error {
		if r == nil {
			return errors.New("build: runner must not be nil")
		}
		o.runner = r
		return nil
	}
}

// WithMeter is where this service says what it accounted for — build minutes
// and the bytes an artifact and a log occupy. Nil emits nothing.
func WithMeter(m Meter) Option {
	return func(o *options) error {
		if m == nil {
			return errors.New("build: a nil meter")
		}
		o.meter = m
		return nil
	}
}

// WithAssets gives this node somewhere to put what a build produced: the log
// file when the run ends, and each artifact as it is built. WITHOUT ONE THE
// NODE STILL BUILDS — the log is written, followed and then thrown away with
// the run, and the job carries no cids — because v2 has no storage service to
// implement Assets yet and a node that refused to build without one could not
// be started at all today. See Assets in config.go for what a real
// implementation owes.
func WithAssets(a Assets) Option {
	return func(o *options) error {
		if a == nil {
			return errors.New("build: asset store must not be nil")
		}
		o.assetStore = a
		return nil
	}
}

// WithAuthorizer replaces the default, which is AllowAnyPeer. See authz.go.
func WithAuthorizer(a Authorizer) Option {
	return func(o *options) error {
		if a == nil {
			return errors.New("build: authorizer must not be nil")
		}
		o.authz = a
		return nil
	}
}

// WithClock replaces the source of time — for this service AND for the queue
// under it, which is the point: a test that moves time has to move both or the
// two disagree about whether a claim has expired.
func WithClock(now func() time.Time) Option {
	return func(o *options) error {
		if now == nil {
			return errors.New("build: clock must not be nil")
		}
		o.now = now
		return nil
	}
}

// New validates and allocates. It binds nothing, opens nothing and creates
// nothing — no listener until Start, no database ever — so a Service can be
// constructed just to be inspected, and a node can construct every service
// before starting any of them (service-conventions.md §2a).
//
// One bag of operator values and everything else injected: the host it answers
// on, the store it persists into, and the one question it may ask auth. Each is
// a With… that refuses nil, so a missing dependency fails at construction and a
// bad one fails where it was written.
//
// It never constructs raft. raft.New takes peer.Node, which bundles host, DHT,
// datastore, DAG and pubsub, and taking it would mean this package could not be
// built against mocknet.GenPeer() with nothing else standing up —
// service_test.go's TestServiceNeedsOnlyAMocknetHostAMockClusterAndAFakeAuth is
// that claim as a test rather than as this sentence.
func New(cfg Config, opts ...Option) (*Service, error) {
	effective, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}

	o := options{authz: AllowAnyPeer(), now: time.Now}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			// Nothing to unwind: New owns no resource by the time an option
			// runs.
			return nil, fmt.Errorf("build: option: %w", err)
		}
	}

	switch {
	case o.host == nil:
		return nil, errors.New("build: no host to answer on")
	case o.store == nil:
		return nil, errors.New("build: no store")
	case o.repos == nil:
		return nil, errors.New("build: no repositories to resolve a project with")
	}

	s := &Service{
		meter:         o.meter,
		host:          o.host,
		store:         o.store,
		repos:         o.repos,
		cfg:           effective,
		runner:        o.runner,
		confirm:       o.confirm,
		assetStore:    o.assetStore,
		authz:         o.authz,
		now:           o.now,
		serverOptions: o.serverOptions,
		running:       map[string]context.CancelFunc{},
		logs:          map[string]runLog{},
	}

	// The queue reads the same clock this service does, so a test that moves
	// time moves both.
	s.group, err = queue.NewGroup(s.store, queue.WithClock(func() time.Time { return s.now() }))
	if err != nil {
		return nil, fmt.Errorf("build: queue group: %w", err)
	}
	s.work, err = s.group.Open(s.cfg.Queue)
	if err != nil {
		return nil, fmt.Errorf("build: queue %q: %w", s.cfg.Queue.Name, err)
	}
	// The effective configuration is the queue's own, defaults filled in — so
	// ApplyTimeout below is the number the queue will actually use rather than
	// a zero this package would have to default a second time.
	s.cfg.Queue = s.work.Config()

	// THE DEAD LETTER IS JUST ANOTHER QUEUE ON THE SAME GROUP, which is what
	// makes draining it a claim/ack like any other. Without a drain, a job that
	// exhausts its attempts sits there forever while its record still reads
	// Open and nothing ever notices.
	s.dead, err = s.group.Open(queue.Config{
		Name:         s.cfg.Queue.DeadLetter,
		ApplyTimeout: s.cfg.Queue.ApplyTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("build: dead letter %q: %w", s.cfg.Queue.DeadLetter, err)
	}

	return s, nil
}

// Start listens and serves, then starts the claim loop. Calling it twice, or
// after Close, is an error rather than a second listener leaking or a silent
// no-op.
//
// Both service blocks mount behind ONE protocol id — build.Protocol, walked off
// the schema — because the id names the proto package's listener. A second
// rpc.Listen on it is refused at bind time with ErrProtocolTaken, so a taken
// protocol is an error rather than a service that starts and is never reached.
func (s *Service) Start(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	if s.closed.Load() {
		return errors.New("build: closed")
	}
	if s.server != nil {
		return errors.New("build: already started")
	}

	server, err := rpc.Listen(ctx, s.host, build.Protocol, s.mount(), s.serverOptions...)
	if err != nil {
		return fmt.Errorf("build: listen on %s: %w", build.Protocol, err)
	}
	s.server = server

	// WITH A CAUSE, because the loop's context ending is not self-explanatory:
	// Close ends it too, and a run that was interrupted by a shutdown reaches
	// run() as an error from Run over a cancelled context — exactly what a build
	// that failed looks like. See errShuttingDown.
	loopCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	s.stop = cancel
	s.stopped = make(chan struct{})
	go s.loop(loopCtx, s.stopped)

	return nil
}

// loop cycles step until the service is closed. It holds no logic of its own —
// everything is in step, which performs exactly one cycle — because that is
// what makes the whole claim path reachable from a test without a goroutine, a
// sleep or a real clock.
func (s *Service) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)

	claim := time.NewTicker(s.cfg.ClaimInterval)
	defer claim.Stop()
	reconcile := time.NewTicker(s.cfg.ReconcileEvery)
	defer reconcile.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-claim.C:
			// A cycle's errors are the queue's own — not the leader, nothing
			// deliverable, a store that refused. There is nobody to return
			// them to and the next cycle is the retry.
			s.step(ctx)
		case <-reconcile.C:
			s.reconcile()
		}
	}
}

// errShuttingDown is the cause Close cancels the claim loop's context with, and
// it exists because NOTHING IN AN ERROR SAYS WHY A CONTEXT DIED. A run stopped
// by a shutdown returns exactly what a run stopped by Cancel returns, and what
// a Runner that wraps ctx.Err() in its own message returns, and what a build
// that genuinely failed at that instant returns. context.Cause on the loop's
// own context is the fact rather than a reading of the error.
var errShuttingDown = errors.New("build: shutting down")

// Close is idempotent and stops the two things this service started: serving,
// and the claim loop. It does not close the store, and cannot — Store has no
// Close method (config.go), because the store was handed in by whatever
// composed the node and is shared with the services composed beside it.
//
// A RUN IN FLIGHT IS ABANDONED, NOT FAILED. The loop's context ends here, so a
// Runner that honours it returns and a shutdown does not wait out an hour-long
// build — but the delivery is NOT acknowledged and no terminal record is
// written. The item stays claimed, the claim expires, and the queue redelivers
// it to whoever is leader then, which is the same path a node that was killed
// takes. A rolling restart therefore costs an attempt and a rebuild; it never
// leaves a job reading Failed for a build nobody ever finished.
//
// Acknowledging it here would look tidier and would be a lie the caller cannot
// detect: the ack "removes an item for good", so there is no redelivery behind
// it, and every restart would permanently fail whatever was building.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)

		s.startMu.Lock()
		stop, stopped, server := s.stop, s.stopped, s.server
		s.startMu.Unlock()

		if stop != nil {
			stop(errShuttingDown)
			<-stopped
		}
		if server != nil {
			s.closeErr = server.Close()
		}
	})
	return s.closeErr
}
