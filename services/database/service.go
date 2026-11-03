// Package database serves a project's kv databases, so compute nodes no longer hold
// them. A database's engine is costly to open, so it is served where it is already
// resident (resident.go).
//
// One model, kv, on both resolutions: crdt over the replicated store every node has,
// and raft over a group per database on a node composed with somewhere to open one.
// Every other model is refused by name, and the refusals are told apart: an unserved
// model is Unimplemented, a wrong surface FailedPrecondition. A crdt copy answers
// from its own; a raft group is answered by its leader (forward.go). If a namespace
// ever ends up with two raft groups, pkg/raft merges them last-writer-wins; the
// promise here is that of one group.
//
// No authorization: any peer that can reach this service can read and write any
// project's data. Swarm membership, enforced by the cloud's CA, is the boundary, as
// for the register and secrets.
package database

import (
	"context"
	"errors"
	"fmt"
	"io"
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
type server struct {
	host    host.Host
	engines *engines
	declare *declarations

	// holdings is what this node has on disk and the only thing that can let
	// go of it. Nil on a node composed without one, which answers held=false
	// to every ask — honest for a node that holds nothing of its own.
	holdings Holdings

	// members is the fleet a placement runs over. Nil on a node that cannot
	// see one, which is a node that never moves anything.
	members func() []placement.Member

	// peer is who this node is in that fleet.
	peer string

	// consensus is what this node can keep: raft needs a group opener, which a
	// composition may or may not hand this service.
	consensus []string

	// warming is which databases this node is currently asking the other
	// desired holders about, so a request path that finds no leader starts one
	// round of asking rather than one per request.
	warmMu  sync.Mutex
	warming map[string]bool

	// ledgersOpen is every raft database this node has opened a group for. It
	// is what this node IS rather than what placement wants it to be -- see
	// forward.go's opening.
	ledgerMu    sync.Mutex
	ledgersOpen map[string]address

	// floor is how many nodes hold each database, read from what the cloud published.
	floor *fleetFloor

	// sweep is how often this node reconsiders what it holds.
	sweep     time.Duration
	stopSweep context.CancelFunc

	// pendingAt is how many of this node's databases were below their floor at
	// the last sweep. One number, gossiped; which ones is asked for directly.
	pendingMu sync.Mutex
	pendingAt int

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

	// owned is what Start opened for this service, closed after it.
	owned []io.Closer
}

// option injects what this service is built from.
type option func(*options) error

type options struct {
	registry *database.Registry
	source   register.Registry
	now      func() time.Time
	max      int
	idle     time.Duration
	holdings Holdings

	members   func() []placement.Member
	sweep     time.Duration
	consensus []string

	serverOptions []rpc.ServerOption
}

// withMembers is the member set a placement runs over, a function since membership
// changes under the service.
func withMembers(members func() []placement.Member) option {
	return func(o *options) error {
		o.members = members
		return nil
	}
}

// withConsensus is what this node can keep. A node without a group opener cannot keep
// a ledger and refuses it by name. The default is crdt alone.
func withConsensus(kinds ...string) option {
	return func(o *options) error {
		for _, kind := range kinds {
			if kind == "" {
				return errors.New("database: an unnamed consensus")
			}
		}
		o.consensus = append(o.consensus, kinds...)
		return nil
	}
}

// withSweep is how often this node reconsiders what it holds. Zero means
// DefaultSweep.
func withSweep(d time.Duration) option {
	return func(o *options) error {
		o.sweep = d
		return nil
	}
}

// withHoldings is what this node has on disk. A service without one answers
// held=false to every ask and can let go of nothing — which is the truth for a
// node that composes no local store.
func withHoldings(h Holdings) option {
	return func(o *options) error {
		o.holdings = h
		return nil
	}
}

// withEngines is what this node can connect to. Required: a database service
// with no engines can answer nothing, and saying so at construction is better
// than answering every call with the same refusal.
func withEngines(r *database.Registry) option {
	return func(o *options) error {
		if r == nil {
			return ErrNoEngines
		}
		o.registry = r
		return nil
	}
}

// withRegister is where declarations are read from. Required, and it is the
// whole of why a caller cannot describe a database — see declarations.go.
func withRegister(source register.Registry) option {
	return func(o *options) error {
		if source == nil {
			return ErrNoRegister
		}
		o.source = source
		return nil
	}
}

// withMaxResident bounds how many engines this node holds at once.
func withMaxResident(n int) option {
	return func(o *options) error {
		if n < 1 {
			return fmt.Errorf("database: %d resident databases is not a bound", n)
		}
		o.max = n
		return nil
	}
}

// withIdle is how long an engine nobody has asked for stays open.
func withIdle(d time.Duration) option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("database: idle %s: an engine closed immediately is never warm", d)
		}
		o.idle = d
		return nil
	}
}

func withClock(now func() time.Time) option {
	return func(o *options) error {
		if now == nil {
			return errors.New("database: a nil clock")
		}
		o.now = now
		return nil
	}
}

// withServerOptions passes p2p/rpc.ServerOption values through to rpc.Listen.
func withServerOptions(opts ...rpc.ServerOption) option {
	return func(o *options) error {
		o.serverOptions = append(o.serverOptions, opts...)
		return nil
	}
}

// New is the database service, unstarted; Start builds it from its instance.
func New() Service { return &server{} }

// build is a service from options alone, as the tests compose one.
func build(h host.Host, opts ...option) (*server, error) {
	s := &server{}
	if err := s.assemble(h, opts...); err != nil {
		return nil, err
	}
	return s, nil
}

// assemble validates and allocates. It binds nothing until serve.
func (s *server) assemble(h host.Host, opts ...option) error {
	if h == nil {
		return ErrNoHost
	}

	o := options{max: DefaultMaxResident, idle: DefaultIdle}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return fmt.Errorf("database: option: %w", err)
		}
	}
	if o.registry == nil {
		return ErrNoEngines
	}
	if o.source == nil {
		return ErrNoRegister
	}

	projects, err := register.NewCache(o.source, specs.Load)
	if err != nil {
		return err
	}

	sweep := o.sweep
	if sweep == 0 {
		sweep = DefaultSweep
	}

	consensus := o.consensus
	if len(consensus) == 0 {
		consensus = servedConsensus
	}

	s.host = h
	s.engines = newEngines(o.registry, o.now, o.max, o.idle)
	s.declare = &declarations{projects: projects}
	s.holdings = o.holdings
	s.members = o.members
	s.peer = h.ID().String()
	s.consensus = consensus
	s.floor = &fleetFloor{from: fleetFrom(o.source)}
	s.sweep = sweep
	s.own = newHoldings()
	s.serverOptions = o.serverOptions
	return nil
}

// serve listens and serves. Calling it twice, or after Close, is an error
// rather than a second listener leaking or a silent no-op.
func (s *server) serve(ctx context.Context) error {
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

	// The sweep lives as long as the service, not the caller's context.
	sweepCtx, stopSweep := context.WithCancel(context.WithoutCancel(ctx))
	s.stopSweep = stopSweep
	go s.run(sweepCtx)

	return nil
}

// Close stops serving and releases every engine this node holds.
func (s *server) Close() error {
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
		for _, c := range s.owned {
			s.closeErr = errors.Join(s.closeErr, c.Close())
		}
	})
	return s.closeErr
}

// Held is how many engines this node has open. It exists for an operator asking
// what a node is holding, and for a test of the bound.
func (s *server) Held() int { return s.engines.held() }

// declared resolves one database to what its author wrote, without opening it: for a
// holding ask, the maintenance sweep, and a write before the release fence. What this
// cloud serves is checked before what the caller asked, so a postgres via KVGet
// reports the unserved model.
func (s *server) declared(ctx context.Context, at address) (declared, error) {
	declaration, err := s.declare.of(ctx, at)
	if err != nil {
		return declared{}, err
	}
	if err := declaration.serves(s.consensus); err != nil {
		return declared{}, err
	}
	if declaration.spec.Type != database.KV {
		return declared{}, fmt.Errorf("%w: %s is a %s", ErrWrongModel, at.id, declaration.spec.Type)
	}
	return declaration, nil
}

// kv resolves one database and answers its kv surface, or says why it cannot.
func (s *server) kv(ctx context.Context, at address) (database.KVConn, error) {
	declaration, err := s.declared(ctx, at)
	if err != nil {
		return nil, err
	}
	return s.open(ctx, at, declaration)
}

// open answers the kv surface of a resolved database. Its consensus alone decides
// where it is answered from (see forward.go).
func (s *server) open(ctx context.Context, at address, declaration declared) (database.KVConn, error) {
	if declaration.spec.Consensus == database.Raft {
		return s.route(ctx, at, declaration)
	}
	return s.here(ctx, at, declaration)
}

// here is this node's own connection to a database, warm.
func (s *server) here(ctx context.Context, at address, declaration declared) (database.KVConn, error) {
	conn, err := s.engines.open(ctx, at.key(), declaration.revision, declaration.spec)
	if err != nil {
		return nil, err
	}

	// Serving a database is holding it, or this node answers keeps=false forever and no
	// move completes.
	s.own.hold(at.key())

	kv, ok := conn.(database.KVConn)
	if !ok {
		// Unreachable while Registry.Register proves an engine's surface; kept so a change
		// there cannot pass silently.
		return nil, fmt.Errorf("%w: %s answers no kv surface", ErrWrongModel, declaration.spec.Engine)
	}
	return kv, nil
}

// mutate runs one write behind the release fence, which protects the gate's answer,
// not the store. The fence is up before the store is opened. Resolving comes first,
// so a peer cannot grow the fence table with undeclared names.
func (s *server) mutate(ctx context.Context, at address, do func(database.KVConn) error) error {
	declaration, err := s.declared(ctx, at)
	if err != nil {
		return err
	}

	key := at.key()
	if !s.own.writing(key) {
		return ErrReleasing
	}
	defer s.own.wrote(key)

	kv, err := s.open(ctx, at, declaration)
	if err != nil {
		return err
	}
	return do(kv)
}

// Local is this service as the compute tier's seam, with no stream; a node not
// running the service uses Client.
func (s *server) Local() wasmtier.KVs { return &localKV{service: s} }

type localKV struct{ service *server }

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
	service *server
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
	return l.service.mutate(ctx, l.at, func(kv database.KVConn) error {
		return kv.Put(ctx, key, value)
	})
}

func (l *localOne) Delete(ctx context.Context, key string) error {
	return l.service.mutate(ctx, l.at, func(kv database.KVConn) error {
		if err := kv.Delete(ctx, key); err != nil && !errors.Is(err, kvengine.ErrNotFound) {
			return err
		}
		return nil
	})
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
