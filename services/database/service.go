// Package database serves a project's databases, and the reason it exists is
// that v1 made the compute node do it.
//
// # What v1 did
//
// The substrate opened the database. Every node that ever served a request for
// a project held that project's connection, its indexes and, for tau's own
// engines, its rows — so the data plane was every node that had ever run code,
// the connection table was unbounded and never expired, and a database's
// warmth was an accident of which node got the traffic. node/database.go is
// still that shape.
//
// # Why it is its own service and not part of storage
//
// data-services.md decided three data services because their requirements
// differ at scale, and the mechanism — rather than the word "hot" — is
// residency. A bucket's current version is DERIVED from its keys, so whichever
// node holds the blocks can answer it. A database's is an open engine, which
// costs more to build than the request that needs it. So a database has to be
// served where its engine already is, and something has to own the engines to
// be able to say where that is. resident.go is that table.
//
// # One cell of the grid, and it says so
//
// The `database` kind is four models — kv, sql, document, vector — on one axis
// and a consensus on another. THIS SERVICE SERVES ONE CELL: tau's own kv over
// the replicated CRDT store. Everything else is refused BY NAME, which is the
// rule node/queue.go already keeps for a crdt queue and node/cache.go for a
// cache, and the one the database kind has been missing entirely while
// `consensus` was RequiredWhen and honoured by nobody.
//
// A service that answered every call it was given and quietly did the wrong
// thing for three quarters of them would be worse than one that refuses, and
// the refusals are told apart: "this service does not serve that model yet" is
// an operator's problem and Unimplemented, while "that database is not a kv" is
// the caller's and FailedPrecondition. declarations.go holds the list, so
// growing it is one edit rather than a sweep.
//
// # What an operator declares about a database node
//
// How much it may hold and for how long: WithMaxResident and WithIdle. Both
// have defaults, because a node that refused to start without them would be a
// node an operator has to tune before it runs at all.
//
// # The thing this package does not do
//
// AUTHORIZATION. Any peer that can reach this service can read and write any
// project's data. That is v2's OSS trust model stated where it applies — swarm
// membership is the boundary and the private swarm key enforces it — and a
// deployment that does not trust its own nodes needs a caller identity on every
// request, not a flag here. The register and the secrets service say the same.
package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/database"
	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/placement"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// Service is the engines this node holds, the declarations it reads them from,
// and the host it answers on.
type Service struct {
	host    host.Host
	engines *engines
	declare *declarations

	// holdings is what this node has on disk and the only thing that can let
	// go of it. Nil on a node composed without one, which answers held=false
	// to every ask — honest for a node that holds nothing of its own.
	holdings Holdings

	// members is the fleet a placement runs over, grouped by region. Nil on a
	// node that cannot see one, which is a node that never moves anything.
	members func() map[string][]placement.Member

	// peer and region are who this node is in that fleet.
	peer   string
	region string

	// floor is how many nodes hold each database in EACH pinned region, read
	// from what the cloud published rather than from this node's config.
	floor *fleetFloor

	// sweep is how often this node reconsiders what it holds.
	sweep     time.Duration
	stopSweep context.CancelFunc

	// own is what this node is DOING about each database it holds, which is
	// what makes its `keeps` answer honest: a node mid-release says so, and a
	// peer cannot count it as a reason to leave too.
	own *holdings

	serverOptions []rpc.ServerOption

	startMu   sync.Mutex
	server    *rpc.Server
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

// Option injects what this service is built from.
type Option func(*options) error

type options struct {
	registry *database.Registry
	source   register.Registry
	now      func() time.Time
	max      int
	idle     time.Duration
	holdings Holdings

	members func() map[string][]placement.Member
	region  string
	sweep   time.Duration

	serverOptions []rpc.ServerOption
}

// WithMembers is how this node sees the fleet: the member set a placement runs
// over, grouped by the region each node reports.
//
// A FUNCTION AND NOT A LIST, because membership changes under the service and a
// snapshot taken at construction would place every database on the fleet as it
// was when the process started.
func WithMembers(members func() map[string][]placement.Member) Option {
	return func(o *options) error {
		o.members = members
		return nil
	}
}

// WithRegion is where this node is. Empty means a node that will not say, which
// competes for nothing anybody pinned.
func WithRegion(region string) Option {
	return func(o *options) error {
		o.region = region
		return nil
	}
}

// WithSweep is how often this node reconsiders what it holds. Zero means
// DefaultSweep.
func WithSweep(d time.Duration) Option {
	return func(o *options) error {
		o.sweep = d
		return nil
	}
}

// WithHoldings is what this node has on disk. A service without one answers
// held=false to every ask and can let go of nothing — which is the truth for a
// node that composes no local store.
func WithHoldings(h Holdings) Option {
	return func(o *options) error {
		o.holdings = h
		return nil
	}
}

// WithEngines is what this node can connect to. Required: a database service
// with no engines can answer nothing, and saying so at construction is better
// than answering every call with the same refusal.
func WithEngines(r *database.Registry) Option {
	return func(o *options) error {
		if r == nil {
			return ErrNoEngines
		}
		o.registry = r
		return nil
	}
}

// WithRegister is where declarations are read from. Required, and it is the
// whole of why a caller cannot describe a database — see declarations.go.
func WithRegister(source register.Registry) Option {
	return func(o *options) error {
		if source == nil {
			return ErrNoRegister
		}
		o.source = source
		return nil
	}
}

// WithMaxResident bounds how many engines this node holds at once.
func WithMaxResident(n int) Option {
	return func(o *options) error {
		if n < 1 {
			return fmt.Errorf("database: %d resident databases is not a bound", n)
		}
		o.max = n
		return nil
	}
}

// WithIdle is how long an engine nobody has asked for stays open.
func WithIdle(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("database: idle %s: an engine closed immediately is never warm", d)
		}
		o.idle = d
		return nil
	}
}

// WithClock injects the local clock. Tests use it; production does not.
func WithClock(now func() time.Time) Option {
	return func(o *options) error {
		if now == nil {
			return errors.New("database: a nil clock")
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

// New validates and allocates. It binds nothing and opens nothing — no listener
// until Start and no engine until a call asks for one — so a node can construct
// every service before starting any of them.
func New(h host.Host, opts ...Option) (*Service, error) {
	if h == nil {
		return nil, ErrNoHost
	}

	o := options{max: DefaultMaxResident, idle: DefaultIdle}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, fmt.Errorf("database: option: %w", err)
		}
	}
	if o.registry == nil {
		return nil, ErrNoEngines
	}
	if o.source == nil {
		return nil, ErrNoRegister
	}

	projects, err := register.NewCache(o.source, specs.Load)
	if err != nil {
		return nil, err
	}

	sweep := o.sweep
	if sweep == 0 {
		sweep = DefaultSweep
	}

	return &Service{
		host:          h,
		engines:       newEngines(o.registry, o.now, o.max, o.idle),
		declare:       &declarations{projects: projects},
		holdings:      o.holdings,
		members:       o.members,
		peer:          h.ID().String(),
		region:        o.region,
		floor:         &fleetFloor{from: fleetFrom(o.source)},
		sweep:         sweep,
		own:           newHoldings(),
		serverOptions: o.serverOptions,
	}, nil
}

// Start listens and serves. Calling it twice, or after Close, is an error
// rather than a second listener leaking or a silent no-op.
func (s *Service) Start(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	if s.closed.Load() {
		return errors.New("database: closed")
	}
	if s.server != nil {
		return errors.New("database: already started")
	}

	server, err := rpc.Listen(ctx, s.host, Protocol, s.mount(), s.serverOptions...)
	if err != nil {
		return fmt.Errorf("database: listening: %w", err)
	}
	s.server = server

	// THE SWEEP RUNS FOR AS LONG AS THIS SERVICE DOES, not for as long as the
	// caller's context: a Start whose ctx ends when the caller returns would
	// leave a node that serves and never reconsiders anything.
	sweepCtx, stopSweep := context.WithCancel(context.WithoutCancel(ctx))
	s.stopSweep = stopSweep
	go s.run(sweepCtx)

	return nil
}

// Close stops serving and releases every engine this node holds.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)

		s.startMu.Lock()
		server := s.server
		s.server = nil
		stop := s.stopSweep
		s.stopSweep = nil
		s.startMu.Unlock()

		if stop != nil {
			stop()
		}

		if server != nil {
			s.closeErr = server.Close()
		}
		s.closeErr = errors.Join(s.closeErr, s.engines.closeAll())
	})
	return s.closeErr
}

// Held is how many engines this node has open. It exists for an operator asking
// what a node is holding, and for a test of the bound.
func (s *Service) Held() int { return s.engines.held() }

// declared resolves one database to what its author wrote, WITHOUT opening it.
//
// IT IS THE HALF kv() DOES FIRST, split out because two callers must not open:
// a node asked whether it holds a database would become a holder by answering,
// and the maintenance sweep decides about databases it may be letting go of.
func (s *Service) declared(ctx context.Context, at address) (declared, error) {
	declaration, err := s.declare.of(ctx, at)
	if err != nil {
		return declared{}, err
	}
	// WHAT THIS CLOUD SERVES, BEFORE WHAT THE CALLER ASKED FOR — see kv().
	if err := declaration.serves(); err != nil {
		return declared{}, err
	}
	if declaration.spec.Type != database.KV {
		return declared{}, fmt.Errorf("%w: %s is a %s", ErrWrongModel, at.id, declaration.spec.Type)
	}
	return declaration, nil
}

// kv resolves one database and answers its kv surface, or says why it cannot.
func (s *Service) kv(ctx context.Context, at address) (database.KVConn, error) {
	declaration, err := s.declare.of(ctx, at)
	if err != nil {
		return nil, err
	}
	// WHAT THIS CLOUD SERVES, BEFORE WHAT THE CALLER ASKED FOR. A postgres
	// reached through KVGet is two different mistakes depending on the order
	// these are checked, and the one worth reporting is that this service does
	// not serve sql — not that the caller used the wrong verb on a database it
	// could never have used the right one on.
	if err := declaration.serves(); err != nil {
		return nil, err
	}
	if declaration.spec.Type != database.KV {
		return nil, fmt.Errorf("%w: %s is a %s", ErrWrongModel, at.id, declaration.spec.Type)
	}

	conn, err := s.engines.open(ctx, at.key(), declaration.revision, declaration.spec)
	if err != nil {
		return nil, err
	}

	// SERVING A DATABASE IS HOLDING IT, and a node that holds one means to keep
	// it until its own release says otherwise. Without this a node answers
	// keeps=false forever and no move anywhere in the fleet could ever complete
	// — the gate would refuse every drop on the word of nodes that all claim to
	// be leaving.
	s.own.hold(at.key())

	kv, ok := conn.(database.KVConn)
	if !ok {
		// Unreachable while Registry.Register proves an engine answers its
		// type's operations. Kept rather than dropped so a change to that rule
		// cannot pass silently through here.
		return nil, fmt.Errorf("%w: %s answers no kv surface", ErrWrongModel, declaration.spec.Engine)
	}
	return kv, nil
}

// Local is this service as the compute tier's own seam, with no stream.
//
// IT IS THE ABSENCE OF A HOP AND NOT A SHORTCUT. A node that runs the database
// service already holds the engines, so dialling itself to reach them would add
// a stream and a serialization to every call — the same rule node/secrets.go
// keeps for a vault and services/worker keeps for a register. A node that does
// NOT run the service uses Client, and nothing above the seam can tell which it
// got.
func (s *Service) Local() wasmtier.KVs { return &localKV{service: s} }

type localKV struct{ service *Service }

var _ wasmtier.KVs = (*localKV)(nil)

func (l *localKV) OpenKV(_ context.Context, grant invoke.Grant) (wasmtier.KV, error) {
	project, environment, id := splitHandle(grant.Handle)
	if project == "" || environment == "" || id == "" {
		return nil, fmt.Errorf("%w: %q", ErrNoID, grant.Handle)
	}
	return &localOne{
		service: l.service,
		at:      address{project: project, environment: environment, id: id},
	}, nil
}

// localOne resolves per call, exactly as the wire does — so what is resident,
// and whether a newer deployment has replaced it, is decided in one place for
// both callers.
type localOne struct {
	service *Service
	at      address
}

func (l *localOne) Get(ctx context.Context, key string) ([]byte, error) {
	kv, err := l.service.kv(ctx, l.at)
	if err != nil {
		return nil, err
	}
	value, err := kv.Get(ctx, key)
	if errors.Is(err, kvengine.ErrNotFound) {
		return nil, fmt.Errorf("%w: %s", wasmtier.ErrNotFound, key)
	}
	return value, err
}

func (l *localOne) Put(ctx context.Context, key string, value []byte) error {
	kv, err := l.service.kv(ctx, l.at)
	if err != nil {
		return err
	}
	return kv.Put(ctx, key, value)
}

func (l *localOne) Delete(ctx context.Context, key string) error {
	kv, err := l.service.kv(ctx, l.at)
	if err != nil {
		return err
	}
	if err := kv.Delete(ctx, key); err != nil && !errors.Is(err, kvengine.ErrNotFound) {
		return err
	}
	return nil
}

func (l *localOne) Keys(ctx context.Context, prefix string) ([]string, error) {
	kv, err := l.service.kv(ctx, l.at)
	if err != nil {
		return nil, err
	}
	return kv.List(ctx, prefix)
}

// fleetFrom is the register's fleet half, when it has one. A source that only
// resolves projects is a legitimate thing to build this service over — it reads
// the default and never changes its mind.
func fleetFrom(source register.Registry) fleetSource {
	from, ok := source.(fleetSource)
	if !ok {
		return nil
	}
	return from
}
