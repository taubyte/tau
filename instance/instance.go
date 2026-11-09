// Package instance is the composition root: it starts the v2 services an operator
// enabled on one machine, in the catalog's order, and gives each what the node holds
// that a service cannot build for itself: the databases it shares, the consensus
// groups it opens, how it finds a peer, the bus, and what whoever composed the node
// handed it.
//
// It binds nothing (Handler returns the gateway's handler for the caller to serve)
// and builds no host, pubsub or datastore, so dream can run several nodes in one
// process.
package instance

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/services/cm"
	databaseService "github.com/taubyte/tau/services/database"
	gateway "github.com/taubyte/tau/services/gateway"
	primitivesService "github.com/taubyte/tau/services/primitives"
	registerService "github.com/taubyte/tau/services/register"
	secretsService "github.com/taubyte/tau/services/secrets"
	statsService "github.com/taubyte/tau/services/stats"
	"github.com/taubyte/tau/services/worker"
	workerHosted "github.com/taubyte/tau/services/worker/hosted"
	"github.com/taubyte/tau/services/worker/stream"
)

// Node is everything this machine runs.
type Instance struct {
	cfg  Config
	opts options

	// peer is what this instance is made from; host and pubsub are its, dbs the
	// replicated databases opened over it.
	peer.Node

	dbs *databases

	// services is what this instance runs; running is every service running here,
	// and what the node gives them, for the others to Get.
	services  []named
	runningMu sync.Mutex
	running   []service.Running

	stats      statsService.Service
	register   registerService.Service
	secrets    secretsService.Service
	database   databaseService.Service
	primitives primitivesService.Service

	// certs is the client every public listener here takes certificates from, and
	// challengeDB is where DNS-01 values are held. All nil in a cloud that issues
	// none.
	certs            *cm.Client
	domainClaims     *cm.Domains
	challengeDB      kvdb.Instance
	challengeAnswers *cm.Challenges

	// bus is this node's one bus, shared by a workload's publish and a browser's
	// attach: both reach the same topic, and a second join of it would be a second
	// mesh, with the browser on the one the workload is not.
	bus *stream.Bus

	// closers are everything opened, in order; Close walks them backwards.
	closersMu sync.Mutex
	closers   []io.Closer

	// finder is how a service here finds a peer: p2p discovery by protocol. served
	// is the node's whole answer, itself first for what it runs.
	finder *finder
	served *service.Served

	// shared is the vault Vault hands out, made on first use.
	sharedMu sync.Mutex
	shared   *secretsService.Vault

	// loopsStop ends the node's own loop, and retentionDone is closed once it has.
	loopsStop     context.CancelFunc
	retentionDone chan struct{}

	startMu   sync.Mutex
	started   bool
	closeOnce sync.Once
	closeErr  error
}

// New builds the services this node runs. It binds nothing and starts nothing.
func New(p peer.Node, cfg Config, opts ...Option) (*Instance, error) {
	installTracerProvider()

	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, fmt.Errorf("node: option: %w", err)
		}
	}

	cfg, err := cfg.withDefaults(o.serviceNames()...)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrNoPeer
	}
	n := &Instance{cfg: cfg, opts: o, Node: p, dbs: newDatabaseSet(p, o.opener)}
	// IN THE CATALOG'S ORDER, which is the order they start in, not the file's.
	for _, name := range o.serviceNames() {
		if !cfg.runs(name) {
			continue
		}
		if s, ok := o.serviceNamed(name); ok {
			n.services = append(n.services, named{name: name, service: s})
			if d, ok := s.(statsService.Service); ok {
				n.stats = d
			}
			if r, ok := s.(registerService.Service); ok {
				n.register = r
			}
			if v, ok := s.(secretsService.Service); ok {
				n.secrets = v
			}
			if p, ok := s.(primitivesService.Service); ok {
				n.primitives = p
			}
			if d, ok := s.(databaseService.Service); ok {
				n.database = d
			}
		}
	}
	// The databases every service on this node shares, and the consensus groups
	// each opens for itself.
	n.provide("", n.dbs)
	n.provide("", groups{n})
	// Where cm looks a domain's proof up, when this node was handed one.
	if o.resolver != nil {
		n.provide("", o.resolver)
	}
	// The certificate a service's own public listener answers with, and DNS-01's.
	n.provide("", certificates{n})
	// The services this node runs, in its report from the first: a node is routed to
	// for them as soon as it says anything.
	n.provide("", configured{n})
	// Every service name a node of this cloud can run, and whether its gateway's
	// handler is served over TLS.
	n.provide("", catalog{n})
	n.provide("", terminating{n})
	// What whoever composed the node handed it for the worker: the tiers it runs on,
	// the database engines it can reach, and where a build's artifact is.
	n.provide("", tiers(o.runtimes))
	if o.engines != nil {
		n.provide("", o.engines)
	}
	if o.artifacts != nil {
		n.provide("", o.artifacts)
	}
	// Closed last: every service closes the databases it opened before them.
	n.closers = append(n.closers, n.dbs)
	// The bus, when the node gossips.
	if n.Messaging() != nil {
		n.bus = stream.NewBus(n.Messaging(), cfg.Cloud)
		n.closers = append(n.closers, n.bus)
		buses, err := stream.NewBuses(n.bus)
		if err != nil {
			n.Close()
			return nil, err
		}
		n.provide("", buses)
		n.provide("", bus{n.bus})
	}
	// How every service here finds a peer serving a protocol.
	n.finder = &finder{n: n}
	n.served = service.NewServed(p.ID(), n.finder, cfg.Clients.Retry)
	n.closers = append(n.closers, n.finder)
	if err := n.openIdentity(); err != nil {
		n.Close()
		return nil, err
	}
	return n, nil
}

// configured is the services this node runs, as its report names them.
type configured struct{ n *Instance }

func (c configured) Report(snap *statsService.Snapshot) {
	snap.Serving = append(snap.Serving, c.n.cfg.Services...)
}

// tiers is the compute tiers whoever composed this node gave it.
type tiers []invoke.Runtime

func (t tiers) Runtimes() []invoke.Runtime { return t }

// bus is the node's bus as the worker takes it.
type bus struct{ b *stream.Bus }

func (b bus) Bus() *stream.Bus { return b.b }

// Start starts every configured service, in the catalog's order, advertises what they
// serve, and follows the region's retention. Calling it twice, or after Close, is an
// error rather than a second listener leaking.
func (n *Instance) Start(ctx context.Context) error {
	n.startMu.Lock()
	defer n.startMu.Unlock()

	if n.started {
		return errors.New("node: already started")
	}
	if err := n.startServices(ctx); err != nil {
		return err
	}
	n.advertise()

	// THE LOOP OUTLIVES THE CALL THAT STARTED IT, so it takes a context of its own
	// that Close cancels.
	loopCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	n.loopsStop = stop
	n.retentionDone = make(chan struct{})
	go func() {
		defer close(n.retentionDone)
		n.followRetention(loopCtx)
	}()

	n.started = true
	return nil
}

// addCloser records something to close with the node. It is safe from any
// goroutine.
func (n *Instance) addCloser(c io.Closer) {
	n.closersMu.Lock()
	n.closers = append(n.closers, c)
	n.closersMu.Unlock()
}

// Handler is what this node serves on the internet, or nil when it runs no
// gateway.
func (n *Instance) Handler() http.Handler {
	g := n.runningGateway()
	if g == nil {
		return nil
	}
	return g.Handler()
}

// TLSConfig is what a composer serving Handler over TLS answers handshakes
// with: each name's certificate is the one its gateway declares. Nil when this
// node runs no gateway.
func (n *Instance) TLSConfig() *tls.Config {
	g := n.runningGateway()
	if g == nil {
		return nil
	}
	return &tls.Config{GetCertificate: g.Certificate, MinVersion: tls.VersionTLS12}
}

// runningGateway is the gateway this node runs, once started, or nil.
func (n *Instance) runningGateway() *gateway.Service {
	g, _ := service.Get[*gateway.Service](&added{node: n})
	return g
}

// runningWorker is the worker this node runs, once started, or nil.
func (n *Instance) runningWorker() *workerHosted.Service {
	w, _ := service.Get[*workerHosted.Service](&added{node: n})
	return w
}

// Close stops everything, in the reverse of the order it was opened. It is
// idempotent, and it closes what was built even when Start was never called.
func (n *Instance) Close() error {
	n.closeOnce.Do(func() {
		if n.loopsStop != nil {
			n.loopsStop()
			<-n.retentionDone
		}
		n.closersMu.Lock()
		closers := append([]io.Closer(nil), n.closers...)
		n.closersMu.Unlock()
		for i := len(closers) - 1; i >= 0; i-- {
			if err := closers[i].Close(); err != nil && n.closeErr == nil {
				n.closeErr = err
			}
		}
	})
	return n.closeErr
}

// Vault is the cloud's secrets as this node reads and writes them: through its own
// service when it runs one, otherwise through whichever nodes the finder names. It
// is made once and closed with the node.
func (n *Instance) Vault() Vault {
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	if n.shared == nil {
		n.shared = secretsService.VaultOn(&added{node: n})
		n.addCloser(n.shared)
	}
	return n.shared
}

// BusTopic is what a bus's handle goes on the wire as. Whatever comes to attach a
// browser has to listen on exactly what a publish reaches.
func (n *Instance) BusTopic(handle string) string {
	return stream.Topic(n.cfg.Cloud, handle)
}

// Services is what this node runs, for an operator asking.
func (n *Instance) Services() []string { return append([]string(nil), n.cfg.Services...) }

// Register is this node's register, or nil.
func (n *Instance) Register() registerService.Service { return n.register }

// Worker is this node's worker once started, or nil.
func (n *Instance) Worker() *worker.Service {
	if w := n.runningWorker(); w != nil {
		return w.Worker()
	}
	return nil
}

// Artifacts is where this node's worker looks a built resource's artifact up, once
// started; nil before.
func (n *Instance) Artifacts() worker.Artifacts {
	if w := n.runningWorker(); w != nil {
		return w.Artifacts()
	}
	return nil
}

// PLMArtifacts is what this node's worker reads a built resource's artifact from,
// plm's records on this node, once started; nil before.
func (n *Instance) PLMArtifacts() worker.Artifacts {
	if w := n.runningWorker(); w != nil {
		return w.PLMArtifacts()
	}
	return nil
}

// Secrets is this node's vault, or nil if it runs none.
func (n *Instance) Secrets() secretsService.Service { return n.secrets }

// Database is this node's database service, or nil if it runs none.
func (n *Instance) Database() databaseService.Service { return n.database }

// Primitives is this node's primitives service, or nil if it runs none.
func (n *Instance) Primitives() primitivesService.Service { return n.primitives }

// Stats is this node's view of the fleet, or nil.
func (n *Instance) Stats() statsService.Service { return n.stats }

// PubSub is the gossip this node was given, for a composer that has to attach
// something to it — a stream subscriber, a test listening for what a workload
// published.
func (n *Instance) PubSub() statsService.PubSub { return n.Messaging() }

// QueueDepth is what one declared queue holds: items waiting and claimed. For
// operators and tests; it reads local state without a barrier. `ok` is false for an
// undeclared handle, an unreachable service, an unformed group or a node running no
// worker alike. A dead letter is read by the handle of the queue named in
// `dead_letter`.
func (n *Instance) QueueDepth(handle string) (ready, claimed int, ok bool) {
	if w := n.runningWorker(); w != nil {
		return w.QueueDepth(handle)
	}
	return 0, 0, false
}

// closerFunc adapts a plain stop function to io.Closer.
type closerFunc func()

func (f closerFunc) Close() error { f(); return nil }
