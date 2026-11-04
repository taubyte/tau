package backend

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/run"
)

// server is build's private half: the host it answers on, the raft group its
// queue lives in, the kvdb database its jobs live in, and what it was given to run
// work with.
type server struct {
	host  host.Host
	store Store
	repos Repositories
	cfg   Config

	// records is where every job's events and rows live (events.go); journal
	// appends to it and folds a job back.
	records kvdb.Instance
	journal *journal

	confirm run.Confirmer

	group *queue.Group
	work  *queue.Queue
	dead  *queue.Queue

	runner run.Runner
	// assetStore is the one door in this package off this machine, named for the door
	// so the censuses can tell it from the record's Assets map and a run's cids.
	assetStore Assets
	authz      Authorizer
	now        func() time.Time

	serverOptions []rpc.ServerOption

	// startMu guards server and the loop's lifetime: Start and Close both
	// read-then-write them, and nothing about this type promises a caller will
	// only ever touch one server from one goroutine.
	startMu sync.Mutex
	server  *rpc.Server
	stop    context.CancelCauseFunc
	stopped chan struct{}

	// running holds the cancel of every job this node runs, so Cancel can stop one in
	// the process that wrote the record.
	runningMu sync.Mutex
	running   map[string]context.CancelFunc

	// logs holds the log of every build this node is writing, for Follow, until the
	// run's record names it. Decided by the run having started, not by a record read.
	logsMu sync.Mutex
	logs   map[string]runLog
	// runs rings when a run starts on this node or a job ends here, for a follow
	// waiting on the leader for the run it asked about.
	runs notice

	// meter is where this service says what it accounted for. Nil is the
	// ordinary case; see meter.go.
	meter Meter

	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

// option configures an options value; New builds the server once from the
// validated snapshot.
type option func(*options) error

type options struct {
	host    host.Host
	store   Store
	records kvdb.Instance
	repos   Repositories

	runner        run.Runner
	confirm       run.Confirmer
	assetStore    Assets
	meter         Meter
	authz         Authorizer
	now           func() time.Time
	serverOptions []rpc.ServerOption
}

// withHost is the libp2p host this service answers on. Required: nil is an
// error, never an invented identity.
func withHost(h host.Host) option {
	return func(o *options) error {
		if h == nil {
			return errors.New("build: no host to answer on")
		}
		o.host = h
		return nil
	}
}

// withStore is the raft group the queue lives in, and only the queue. Required.
func withStore(st Store) option {
	return func(o *options) error {
		if st == nil {
			return errors.New("build: no store")
		}
		o.store = st
		return nil
	}
}

// withRecords is the kvdb database every job's events and rows live in. Required.
func withRecords(db kvdb.Instance) option {
	return func(o *options) error {
		if db == nil {
			return errors.New("build: no records")
		}
		o.records = db
		return nil
	}
}

// withRepositories resolves a project. Required.
func withRepositories(r Repositories) option {
	return func(o *options) error {
		if r == nil {
			return errors.New("build: no repositories to resolve a project with")
		}
		o.repos = r
		return nil
	}
}

// withConfirmer is what a push is checked against before it is queued: the
// repository itself, asked whether the ref is at the commit. Without it a push
// is queued as the frontend verified it.
func withConfirmer(c run.Confirmer) option {
	return func(o *options) error {
		if c == nil {
			return errors.New("build: confirmer must not be nil")
		}
		o.confirm = c
		return nil
	}
}

// withServerOptions passes p2p/rpc.ServerOption values through to rpc.Listen
// in Start without this package needing an option of its own for each one.
func withServerOptions(opts ...rpc.ServerOption) option {
	return func(o *options) error {
		o.serverOptions = append(o.serverOptions, opts...)
		return nil
	}
}

// withRunner gives this node something to run claimed work with. Without one the
// node claims nothing but still serves, replicates and sweeps: a voter need not
// be a builder.
func withRunner(r run.Runner) option {
	return func(o *options) error {
		if r == nil {
			return errors.New("build: runner must not be nil")
		}
		o.runner = r
		return nil
	}
}

// withMeter is where this service says what it accounted for — build minutes
// and the bytes an artifact and a log occupy. Nil emits nothing.
func withMeter(m Meter) option {
	return func(o *options) error {
		if m == nil {
			return errors.New("build: a nil meter")
		}
		o.meter = m
		return nil
	}
}

// withAssets gives this node somewhere to put logs and artifacts. Without one the
// node still builds, but logs die with the run and jobs carry no cids (see
// Assets in config.go).
func withAssets(a Assets) option {
	return func(o *options) error {
		if a == nil {
			return errors.New("build: asset store must not be nil")
		}
		o.assetStore = a
		return nil
	}
}

// withAuthorizer replaces the default, which is AllowAnyPeer. See authz.go.
func withAuthorizer(a Authorizer) option {
	return func(o *options) error {
		if a == nil {
			return errors.New("build: authorizer must not be nil")
		}
		o.authz = a
		return nil
	}
}

// withClock replaces the source of time — for this service AND for the queue
// under it, which is the point: a test that moves time has to move both or the
// two disagree about whether a claim has expired.
func withClock(now func() time.Time) option {
	return func(o *options) error {
		if now == nil {
			return errors.New("build: clock must not be nil")
		}
		o.now = now
		return nil
	}
}

// newServer validates and allocates; it binds and opens nothing. Each dependency comes
// through an option that refuses nil. It never constructs raft, so the package builds
// against mocknet.GenPeer(). Start builds one from an instance; a test builds one
// from what it fakes.
func newServer(cfg Config, opts ...option) (*server, error) {
	effective, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}

	o := options{authz: AllowAnyPeer(), now: time.Now}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			// Nothing to unwind: newServer owns no resource by the time an option
			// runs.
			return nil, fmt.Errorf("build: option: %w", err)
		}
	}

	switch {
	case o.host == nil:
		return nil, errors.New("build: no host to answer on")
	case o.store == nil:
		return nil, errors.New("build: no store")
	case o.records == nil:
		return nil, errors.New("build: no records")
	case o.repos == nil:
		return nil, errors.New("build: no repositories to resolve a project with")
	}

	s := &server{
		meter:         o.meter,
		host:          o.host,
		store:         o.store,
		records:       o.records,
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

	s.journal = newJournal(s.records, s.host.ID().String(), func() time.Time { return s.now() })

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

	// The dead letter is another queue on the same group, drained by an ordinary
	// claim and ack.
	s.dead, err = s.group.Open(queue.Config{
		Name:         s.cfg.Queue.DeadLetter,
		ApplyTimeout: s.cfg.Queue.ApplyTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("build: dead letter %q: %w", s.cfg.Queue.DeadLetter, err)
	}

	return s, nil
}

// serve listens, serves, and starts the claim loop; twice or after Close is an
// error. Both service blocks share build.Protocol; a second rpc.Listen on it
// fails with ErrProtocolTaken.
func (s *server) serve(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	if s.closed.Load() {
		return errors.New("build: closed")
	}
	if s.server != nil {
		return errors.New("build: already started")
	}

	listener, err := rpc.Listen(ctx, s.host, build.Protocol, s.mount(), s.serverOptions...)
	if err != nil {
		return fmt.Errorf("build: listen on %s: %w", build.Protocol, err)
	}
	s.server = listener

	// With a cause, since a run interrupted by shutdown otherwise looks like a failed
	// build (errShuttingDown).
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
func (s *server) loop(ctx context.Context, done chan<- struct{}) {
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

// errShuttingDown is the cause Close cancels the loop with: no error says why a
// context died, but context.Cause does.
var errShuttingDown = errors.New("build: shutting down")

// Close is idempotent and stops serving and the claim loop; the store is not its
// to close. A run in flight is abandoned, not failed: unacked, its claim expires
// and the queue redelivers it, so a restart costs a rebuild, never a false
// Failed.
func (s *server) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)

		s.startMu.Lock()
		stop, stopped, listener := s.stop, s.stopped, s.server
		s.startMu.Unlock()

		if stop != nil {
			stop(errShuttingDown)
			<-stopped
		}
		if listener != nil {
			s.closeErr = listener.Close()
		}
	})
	return s.closeErr
}
