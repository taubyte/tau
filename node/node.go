// Package node is the composition root: it starts the v2 services an operator
// enabled on one machine and wires them to each other.
//
// # What a composition root is for
//
// Every v2 service takes narrow interfaces and builds nothing itself — a worker
// takes a register it can read, a gateway takes something that can invoke, a
// register takes a store. That is what makes each of them testable against a
// map and a mocknet, and it leaves exactly one thing undone: somebody has to
// decide what is connected to what. This is that somebody, and it is the only
// place in v2 that names more than one service.
//
// # A node reports what it runs, and everything routes on that
//
// The one non-obvious wire is stats. A node broadcasts the services it runs;
// every locator then reads that same view — a worker looking for a register, a
// gateway looking for a worker — so there is one answer to "who does this" and
// it is a local read on the request path rather than a lookup.
//
// It also means a node that runs a service AND needs it uses it directly, with
// no stream to itself, without any of them knowing whether that happened.
//
// # What it does not do
//
// IT BINDS NOTHING. Handler() returns the gateway's http.Handler and the caller
// serves it, because where and with what certificate is the caller's — see the
// gateway's own doc on why it does not terminate TLS.
//
// IT DOES NOT BUILD A HOST, a pubsub or a datastore. Those are one machine's
// resources shared by everything on it, and a composition that built its own
// would make a second node in one process impossible — which is exactly what
// dream does.
package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/static"
	"github.com/taubyte/tau/pkg/project"

	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/frontend"
	registerService "github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/stats"
	authService "github.com/taubyte/tau/services/v2/auth"
	gateway "github.com/taubyte/tau/services/v2/gateway"
	"github.com/taubyte/tau/services/v2/placement"
	"github.com/taubyte/tau/services/worker"
)

// Node is everything this machine runs.
type Node struct {
	cfg  Config
	opts options

	stats    *stats.Service
	register *registerService.Service
	worker   *worker.Service
	gateway  *gateway.Service

	auth          *authService.Service
	buildBackend  *backend.Service
	buildFrontend *frontend.Service

	pool    *invoke.Pool
	workers *placement.Workers

	// closers are everything opened during construction, in the order it was
	// opened. Close walks them backwards.
	closers []io.Closer

	// registerMu guards the stream to a remote register, which is opened on
	// first use and re-opened if the node it names changes.
	registerMu     sync.Mutex
	registerClient *registerService.Client
	registerAt     libp2ppeer.ID

	startMu   sync.Mutex
	started   bool
	closeOnce sync.Once
	closeErr  error
}

// New builds the services this node runs. It binds nothing and starts nothing.
func New(cfg Config, opts ...Option) (*Node, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}

	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, fmt.Errorf("node: option: %w", err)
		}
	}
	if o.host == nil {
		return nil, ErrNoHost
	}

	n := &Node{cfg: cfg, opts: o}
	if err := n.open(); err != nil {
		n.Close()
		return nil, err
	}
	return n, nil
}

// open constructs in dependency order. Anything opened is recorded as it is
// opened, so a failure half way closes what it made.
func (n *Node) open() error {
	// STATS FIRST. Everything else routes on the view it maintains, including
	// the locators below, so a node that runs it must have it before they are
	// wired.
	if err := n.openStats(); err != nil {
		return err
	}
	if err := n.openRegister(); err != nil {
		return err
	}
	if err := n.openAuth(); err != nil {
		return err
	}
	if err := n.openWorker(); err != nil {
		return err
	}
	if err := n.openGateway(); err != nil {
		return err
	}
	// THE TWO BUILD ROLES COME LAST because both dial auth, and a node running
	// auth itself has to have it answering before a pool onto it is opened.
	if err := n.openBuildBackend(); err != nil {
		return err
	}
	return n.openBuildFrontend()
}

func (n *Node) openStats() error {
	if !n.cfg.runs(Stats) {
		return nil
	}
	if n.opts.pubsub == nil {
		return ErrNoPubSub
	}

	bus, err := stats.NewBus(n.opts.pubsub, n.cfg.Cloud)
	if err != nil {
		return err
	}

	options := []stats.Option{stats.WithBus(bus), stats.WithReporter(n)}
	if n.opts.now != nil {
		options = append(options, stats.WithClock(n.opts.now))
	}

	svc, err := stats.New(n.opts.host, n.cfg.Stats, options...)
	if err != nil {
		return err
	}
	n.stats = svc
	n.closers = append(n.closers, svc)
	return nil
}

func (n *Node) openRegister() error {
	if !n.cfg.runs(Register) {
		return nil
	}
	if n.opts.databases == nil {
		return ErrNoDatabases
	}

	db, err := n.opts.databases.New(logger, Register, rebroadcastSeconds)
	if err != nil {
		return err
	}
	n.closers = append(n.closers, closerFunc(db.Close))

	store, err := registerService.NewKVStore(db)
	if err != nil {
		return err
	}

	svc, err := registerService.New(n.opts.host, registerService.WithStore(store))
	if err != nil {
		return err
	}
	n.register = svc
	n.closers = append(n.closers, svc)
	return nil
}

func (n *Node) openWorker() error {
	if !n.cfg.runs(Worker) {
		return nil
	}
	tiers := append([]invoke.Runtime(nil), n.opts.runtimes...)

	// A DECLARED TIER IS BUILT HERE; an injected one was built by whoever
	// composed this. Both end up in the same pool, and a node can have both —
	// a container backend an operator named and a wasm engine the process
	// already holds.
	if n.cfg.Container.declared() {
		tier, err := containerTier(n.cfg.Container)
		if err != nil {
			return err
		}
		tiers = append(tiers, tier)
	}

	// A SITE NEEDS NO DAEMON AND NO OPERATOR CHOICE. Its cold start is a fetch
	// and an unpack, so a node that can reach the asset store can serve one —
	// which means a static website runs on the smallest possible cloud, and
	// that is most of what an imported project is.
	if n.opts.assets != nil {
		site, err := static.New(n.opts.assets)
		if err != nil {
			return err
		}
		tiers = append(tiers, site)
	}

	if len(tiers) == 0 {
		return errors.New("node: a worker with no compute tier can run nothing; declare a container backend, give it an asset store, or inject one")
	}

	pool, err := invoke.NewPool(invoke.WithRuntime(tiers...))
	if err != nil {
		return err
	}
	n.pool = pool
	n.closers = append(n.closers, closerFunc(func() { pool.Close() }))

	options := []worker.Option{
		worker.WithRegister(n.registerSource()),
		worker.WithPool(pool),
	}
	// WHAT A BUILT RESOURCE RUNS COMES FROM THE BUILD SERVICE, unless a
	// composer injected something else. An injected one wins because it is the
	// more specific statement: somebody who handed this node an artifact
	// resolver meant that one.
	switch {
	case n.opts.artifacts != nil:
		options = append(options, worker.WithArtifacts(n.opts.artifacts))
	case n.stats != nil:
		options = append(options, worker.WithArtifacts(&artifacts{node: n, seen: map[string]builtAt{}}))
	}
	if n.opts.secrets != nil {
		options = append(options, worker.WithSecrets(n.opts.secrets))
	}
	if n.opts.now != nil {
		options = append(options, worker.WithClock(n.opts.now))
	}

	svc, err := worker.New(n.opts.host, n.cfg.Worker, options...)
	if err != nil {
		return err
	}
	n.worker = svc
	n.closers = append(n.closers, svc)
	return nil
}

func (n *Node) openGateway() error {
	if !n.cfg.runs(Gateway) {
		return nil
	}
	if n.stats == nil {
		return errors.New("node: a gateway routes from stats, and this node does not run one")
	}

	options := []placement.Option{}
	if n.worker != nil {
		options = append(options, placement.WithLocal(n.worker))
	}

	workers, err := placement.New(n.opts.host, n.stats, options...)
	if err != nil {
		return err
	}
	n.workers = workers
	n.closers = append(n.closers, workers)

	source := n.registerSource()
	gatewayOptions := []gateway.Option{
		gateway.WithHosts(source),
		gateway.WithSource(source),
		gateway.WithWorkers(workers),
	}
	if n.opts.now != nil {
		gatewayOptions = append(gatewayOptions, gateway.WithClock(n.opts.now))
	}

	svc, err := gateway.New(n.cfg.Gateway, gatewayOptions...)
	if err != nil {
		return err
	}
	n.gateway = svc
	return nil
}

// registerSource is how this node reads the register: directly when it runs
// one, and over the swarm otherwise.
//
// THE LOCAL CASE IS NOT AN OPTIMISATION SO MUCH AS THE ABSENCE OF A HOP. A
// register is a state machine over a replicated store, so a node running one
// already has every answer locally; dialling itself to ask would add a stream
// and a serialization to a read that happens on every request.
func (n *Node) registerSource() *source {
	return &source{node: n}
}

// Start starts what was built, in the order it was built. Calling it twice, or
// after Close, is an error rather than a second listener leaking.
func (n *Node) Start(ctx context.Context) error {
	n.startMu.Lock()
	defer n.startMu.Unlock()

	if n.started {
		return errors.New("node: already started")
	}

	// STATS FIRST, for the same reason it is built first: everything else
	// routes on the view it maintains.
	var order []starter
	if n.stats != nil {
		order = append(order, n.stats)
	}
	if n.register != nil {
		order = append(order, n.register)
	}
	if n.worker != nil {
		order = append(order, n.worker)
	}
	if n.auth != nil {
		order = append(order, n.auth)
	}
	// THE BACKEND BEFORE THE FRONTEND, in the one process that could have both
	// — which is none, because withDefaults refuses it — and in the order that
	// reads correctly anyway: the public role's first act is to queue into the
	// private one.
	if n.buildBackend != nil {
		order = append(order, n.buildBackend)
	}
	if n.buildFrontend != nil {
		order = append(order, n.buildFrontend)
	}

	for _, s := range order {
		if err := s.Start(ctx); err != nil {
			return err
		}
	}

	n.started = true
	return nil
}

// Handler is what this node serves on the internet, or nil when it runs no
// gateway.
func (n *Node) Handler() http.Handler {
	if n.gateway == nil {
		return nil
	}
	return n.gateway.Handler()
}

// Close stops everything, in the reverse of the order it was opened. It is
// idempotent, and it closes what was built even when Start was never called.
func (n *Node) Close() error {
	n.closeOnce.Do(func() {
		n.closeRegisterClient()
		for i := len(n.closers) - 1; i >= 0; i-- {
			if err := n.closers[i].Close(); err != nil && n.closeErr == nil {
				n.closeErr = err
			}
		}
	})
	return n.closeErr
}

// Snapshot is what this node says about itself, which is what everything else
// routes on.
//
// LOAD IS ZERO AND SAID TO BE. Nothing here measures it yet, and a made-up
// number would steer traffic on fiction — zero says "as available as anything
// else", which in a fleet of nodes that all report zero is true.
func (n *Node) Snapshot(context.Context) (stats.Snapshot, error) {
	addrs := make([]string, 0, len(n.opts.host.Addrs()))
	for _, a := range n.opts.host.Addrs() {
		addrs = append(addrs, a.String())
	}

	return stats.Snapshot{
		Ready:     true,
		Serving:   n.serving(),
		Addresses: addrs,
	}, nil
}

// serving is every name this node answers to: the services it runs, plus what
// each of them can say about itself.
func (n *Node) serving() []string {
	out := append([]string(nil), n.cfg.Services...)
	if n.worker != nil {
		out = append(out, n.worker.Serving()...)
	}
	return dedupe(out)
}

// Services is what this node runs, for an operator asking.
func (n *Node) Services() []string { return append([]string(nil), n.cfg.Services...) }

// Register is this node's register, or nil.
func (n *Node) Register() *registerService.Service { return n.register }

// Worker is this node's worker, or nil.
func (n *Node) Worker() *worker.Service { return n.worker }

// Stats is this node's view of the fleet, or nil.
func (n *Node) Stats() *stats.Service { return n.stats }

// source reads the register for this node's own services.
type source struct{ node *Node }

var (
	_ project.Registry = (*source)(nil)
	_ gateway.Hosts    = (*source)(nil)
)

func (s *source) Resolve(ctx context.Context, name, environment string) (pkgregister.Pointer, error) {
	c, err := s.client()
	if err != nil {
		return pkgregister.Pointer{}, err
	}
	return c.Resolve(ctx, name, environment)
}

func (s *source) Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error) {
	c, err := s.client()
	if err != nil {
		return nil, err
	}
	return c.Object(ctx, id)
}

func (s *source) ResolveHost(ctx context.Context, fqdn string) (string, string, error) {
	c, err := s.client()
	if err != nil {
		return "", "", err
	}
	return c.ResolveHost(ctx, fqdn)
}
