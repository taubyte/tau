// Package register serves pkg/register over p2p: three RPCs, declared in
// pkg/proto/tau/register/v2/register.proto.
//
// v1 called this tns. The name said name resolution; the job is holding what
// has been published and answering lookups, which is a register.
//
// # This package is a skin, and deliberately a thin one
//
// Everything the register MEANS — content addressing, the pointer/object
// split, a revision that never goes backwards, a key a name cannot escape —
// is pkg/register, a state machine over a map with no host, no stream and no
// goroutine in it. So all of that is a unit test there, and what is left here
// is: decode a request, call it, map a Go error onto a wire status.
//
// That split is why there is no second copy of any rule below. A handler that
// checked a revision itself would be a second opinion about revisions, and
// the day the two disagreed the one that answered would be whichever the
// request happened to reach.
//
// # What an operator declares about a register node
//
// Nothing yet, which is why this package has no Config. The register's whole
// behaviour comes from the store it is given and the requests it receives, and
// a Config struct with no fields in it would be a slot inviting one.
//
// Replication is the STORE's question, not this service's — pkg/register's
// package doc says so and means it. A node handed a CRDT-backed store is a
// replica of the whole register; a node handed a map is a single-node
// development register. Neither is a mode this package knows about.
package register

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/register"
)

// Service is the register as an instance runs it: the cloud's projects, what each
// deployment runs, and the fleet's own document.
type Service interface {
	service.Service
	Fleet

	// Local is the register this service answers from, for the services beside it.
	Local() *register.Register
}

// New is the register service, unstarted; Start builds it from its instance.
func New() Service { return &server{} }

// server is the register's state: the host it answers on and the register it
// answers from.
type server struct {
	host host.Host
	reg  *register.Register

	serverOptions []rpc.ServerOption

	// db is the database Start opened for the store, closed with the service.
	db kvdb.Instance

	// watches is every reader's session on this node; see watch.go.
	watches *watches

	// startMu guards server: Start and Close both read-then-write it, and
	// nothing about this type promises one Service is only ever touched from
	// one goroutine.
	startMu   sync.Mutex
	server    *rpc.Server
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

// option injects what this service is built from. Operator declarations would
// go in a Config; there are none, so everything here is a live collaborator.
type option func(*options) error

type options struct {
	store         register.Store
	serverOptions []rpc.ServerOption
	watchLimits   *watchLimits
}

// withStore is where objects and pointers live. Required.
//
// It takes a STORE rather than a built *register.Register because the register
// is a pure value over a store and there is nothing for a composer to decide
// about it. What a composer does decide is where the rows go — a map for a
// development node, a replicated store for a cluster — and that is this
// argument.
func withStore(s register.Store) option {
	return func(o *options) error {
		if s == nil {
			return ErrNoStore
		}
		o.store = s
		return nil
	}
}

// withWatchLimits bounds this node's reader sessions.
func withWatchLimits(l watchLimits) option {
	return func(o *options) error {
		o.watchLimits = &l
		return nil
	}
}

// WithServerOptions passes p2p/rpc.ServerOption values through to rpc.Listen
// in Start without this package needing an option of its own for each one.
func withServerOptions(opts ...rpc.ServerOption) option {
	return func(o *options) error {
		o.serverOptions = append(o.serverOptions, opts...)
		return nil
	}
}

var (
	ErrNoStore = errors.New("register: a register with no store holds nothing")
	ErrNoHost  = errors.New("register: no host to answer on")
)

// build is a server over a store, not yet serving. A nil host is an error rather
// than a node this service invents for itself.
func build(h host.Host, opts ...option) (*server, error) {
	s := &server{}
	if err := s.assemble(h, opts...); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *server) assemble(h host.Host, opts ...option) error {
	if h == nil {
		return ErrNoHost
	}
	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return fmt.Errorf("register: option: %w", err)
		}
	}
	if o.store == nil {
		return ErrNoStore
	}
	if s.watches == nil {
		limits := defaultWatchLimits
		if o.watchLimits != nil {
			limits = *o.watchLimits
		}
		s.watches = newWatches(limits)
	}
	reg, err := register.New(register.WithStore(observed{Store: o.store, w: s.watches}))
	if err != nil {
		// Unreachable while o.store is checked above. Kept rather than
		// dropped so a change to pkg/register's construction rules cannot
		// pass silently through here.
		return err
	}
	s.host, s.reg, s.serverOptions = h, reg, o.serverOptions
	return nil
}

// serve listens and serves. Calling it twice, or after Close, is an error
// rather than a second listener leaking or a silent no-op.
func (s *server) serve(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	if s.closed.Load() {
		return errors.New("register: closed")
	}
	if s.server != nil {
		return errors.New("register: already started")
	}

	server, err := rpc.Listen(ctx, s.host, Protocol, s.mount(), s.serverOptions...)
	if err != nil {
		return fmt.Errorf("register: listen on %s: %w", Protocol, err)
	}
	s.server = server
	return nil
}

// Close is idempotent and stops the one thing this service started: serving.
// It does not close the store, and cannot — register.Store has no Close,
// because the store was handed in by whatever composed the node and is shared
// with the services composed beside it.
func (s *server) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.startMu.Lock()
		if s.server != nil {
			s.closeErr = s.server.Close()
		}
		s.startMu.Unlock()
		if s.db != nil {
			s.db.Close()
		}
		if s.watches != nil {
			s.watches.close()
		}
	})
	return s.closeErr
}

// Local is the register this service answers from.
//
// IT IS FOR A COMPOSER ON THE SAME NODE, and it is the absence of a hop rather
// than an optimisation: a register is a state machine over a replicated store,
// so a node running one already holds every answer, and dialling itself would
// add a stream and a serialization to a read that happens on every request.
//
// A caller that is NOT on this node has no way to reach this and must use a
// Client, which is the point — the read surface is identical either way, so
// nothing downstream can tell which it got.
func (s *server) Local() *register.Register { return s.reg }

// ResolveFleet and Object are the fleet's document as this register holds it.
func (s *server) ResolveFleet(ctx context.Context) (register.Pointer, error) {
	return s.reg.ResolveFleet(ctx)
}

func (s *server) Object(ctx context.Context, id register.ObjectID) ([]byte, error) {
	return s.reg.Object(ctx, id)
}

// ResolveView and ResolveHost are what a node serves, as this register holds it.
func (s *server) ResolveView(ctx context.Context, project, environment, destination string) (register.ViewPointer, error) {
	return s.reg.ResolveView(ctx, project, environment, destination)
}

func (s *server) ResolveHost(ctx context.Context, fqdn string) (string, string, error) {
	return s.reg.ResolveHost(ctx, fqdn)
}
