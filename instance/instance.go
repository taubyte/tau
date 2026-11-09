// Package instance is the composition root: it starts the v2 services an operator
// enabled on one machine and wires them together, the only place in v2 that names
// more than one service.
//
// A node broadcasts the services it runs, and every locator reads that same view, so
// "who does this" is a local read. A node running a service it needs uses it directly.
//
// It binds nothing (Handler returns the gateway's handler for the caller to serve)
// and builds no host, pubsub or datastore, so dream can run several nodes in one
// process.
package instance

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/taubyte/tau/services/plm/jobs"
	storageService "github.com/taubyte/tau/services/storage"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/js"
	"github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/services/worker/admit"
	"github.com/taubyte/tau/services/worker/cache"
	"github.com/taubyte/tau/services/worker/model"
	"github.com/taubyte/tau/services/worker/primitive"
	"github.com/taubyte/tau/services/worker/stream"
	"github.com/taubyte/tau/services/worker/volume"

	"github.com/taubyte/tau/instance/placement"
	"github.com/taubyte/tau/services/cm"
	cmService "github.com/taubyte/tau/services/cm"
	databaseService "github.com/taubyte/tau/services/database"
	gateway "github.com/taubyte/tau/services/gateway"
	primitivesService "github.com/taubyte/tau/services/primitives"
	v2proxy "github.com/taubyte/tau/services/proxy"
	registerService "github.com/taubyte/tau/services/register"
	secretsService "github.com/taubyte/tau/services/secrets"
	statsService "github.com/taubyte/tau/services/stats"
	"github.com/taubyte/tau/services/worker"
	"github.com/taubyte/tau/services/worker/objects"
)

// Node is everything this machine runs.
type Instance struct {
	cfg  Config
	opts options

	// peer is what this instance is made from; host and pubsub are its, dbs the
	// replicated databases opened over it.
	peer.Node

	dbs *databases

	// store is the storage service as this node's services reach it: builds,
	// their logs, checkpoints and objects are kept there, and read back by cid.
	store *storageService.Reached

	// services is what this instance runs beside its built-ins; running is every
	// service running here, for the others to Get.
	services  []named
	runningMu sync.Mutex
	running   []service.Running

	stats    statsService.Service
	register registerService.Service
	worker   *worker.Service
	gateway  *gateway.Service
	proxy    *v2proxy.Service

	secrets secretsService.Service

	// certs is the client every public listener here takes certificates from, and
	// challengeDB is where DNS-01 values are held. All nil in a cloud that issues
	// none.
	certs            *cmService.Client
	domainClaims     *cmService.Domains
	challengeDB      kvdb.Instance
	challengeAnswers *cm.Challenges

	// database is the service that owns this project's engines, when this node
	// runs it. A node that does not runs no database of its own — it reaches
	// one in the fleet, which is the whole reason the service exists.
	database databaseService.Service

	// primitives is the service that owns this project's `sync` kind — its
	// queues and its logs — when this node runs it. A node that does not
	// reaches one in the fleet; see instance/primitives.go for why there is no
	// third answer.
	primitives primitivesService.Service

	// decider is this node's component tier, narrowed to the two worlds its own
	// policy drives — see decides in authorizer.go. Nil on a node with no asset
	// store, which is a node that runs no components.
	decider decides

	// vault is this node's one reader of secret values, shared by the worker
	// and the wasm tier. It holds a stream to a remote secrets service, so a
	// second would be a second stream to the same peer.
	vault *secretsService.Vault

	// disks is this node's volume backing: a directory per handle under the
	// root, checkpointed into the asset store. Nil on a node with no root,
	// which is a node that mounts nothing.
	disks *volume.Volumes

	// admit decides whether one instance of a store that holds many may exist.
	// It is shared by the wasm tier and the gateway: a workload opening
	// `chat/room-42` and a browser attaching to `/room-42` ask one question.
	admit *admit.Admissions

	// bus is this node's one bus backing, shared by the wasm tier's publish and
	// the gateway's attach. Both reach the same topic, and a second would be a
	// second join of it — a mesh each, with the browser on the one the workload
	// is not.
	bus *stream.Bus

	// subscriptions is the consumers delivering streams on a log to this node's
	// subscribers, matched to them by subscribe.
	subscriptions *stream.Subscriptions

	// syncs is this node's one reach into the primitives service, shared by the
	// wasm tier's `send` and `append` and the dispatcher's claim. A second
	// would be a second stream to the same peer.
	syncs *primitive.Backing

	pool     *invoke.Pool
	runtimes []invoke.Runtime
	workers  *placement.Workers

	// closers are everything opened, in order; Close walks them backwards. Two openers
	// run lazily on the request path, so the slice is guarded by closersMu.
	closersMu sync.Mutex
	closers   []io.Closer

	// finder is how a service here finds a peer: p2p discovery by protocol. served
	// is the node's whole answer, itself first for what it runs.
	finder *finder
	served *service.Served

	// shared is the vault Vault hands out, made on first use.
	sharedMu sync.Mutex
	shared   *secretsService.Vault

	// The locators, each one stream opened on first use: registers for configuration
	// reads, sends for queues, logs and claims.
	registers *rpc.Reacher[registerSeam]
	// views is the register as this node's services read what they serve.
	views *registerService.Views
	sends *rpc.Reacher[primitive.Seam]

	// loopsStop ends the background loops this node runs, and each Done is
	// closed when that loop has stopped. All three are nil on a node that runs
	// neither a worker nor consensus.
	loopsStop      context.CancelFunc
	dispatchDone   chan struct{}
	subscribeDone  chan struct{}
	proxyDone      chan struct{}
	checkpointDone chan struct{}
	retentionDone  chan struct{}

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
			// THE BUILT-INS STILL TAKE STATS AT CONSTRUCTION, until they are
			// services and reach it themselves.
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
	// What the built-ins add to this node's report.
	n.provide("", builtIns{n})
	// The certificate a service's own public listener answers with, and DNS-01's.
	n.provide("", certificates{n})
	// Every service name a node of this cloud can run.
	n.provide("", catalog{n})
	// How every service here finds a peer serving a protocol.
	n.finder = &finder{n: n}
	n.served = service.NewServed(p.ID(), n.finder, cfg.Clients.Retry)
	n.closers = append(n.closers, n.finder)
	// And the workloads the clock fires through, while the worker is one of them.
	if cfg.runs(Worker) {
		n.provide("", workloads{n})
	}
	// Closed last: every service closes the databases it opened before them.
	n.closers = append(n.closers, n.dbs)
	if err := n.open(); err != nil {
		n.Close()
		return nil, err
	}
	// The worker beside the services that read what it holds, and its component tier.
	if n.worker != nil {
		n.provide(Worker, n.worker)
	}
	if n.decider != nil {
		n.provide("", n.decider)
	}
	return n, nil
}

// open constructs in dependency order. Anything opened is recorded as it is
// opened, so a failure half way closes what it made.
func (n *Instance) open() error {
	// The locators first: each closure reads a node field at call time, so services
	// opened later are still found.
	n.openReach()

	if err := n.openIdentity(); err != nil {
		return err
	}
	if err := n.openWorker(); err != nil {
		return err
	}
	// AFTER THE WORKER AND BEFORE THE GATEWAY, because it takes the first as a
	// collaborator and the second takes it as one.
	if err := n.openPlacement(); err != nil {
		return err
	}
	if err := n.openGateway(); err != nil {
		return err
	}
	// AFTER PLACEMENT AND THE WORKER, which it takes as collaborators: the
	// first is where a connection goes, the second is what this node holds.
	return n.openProxy()
}

// tiers is what this node's pool can run. It is here for the composition's own
// tests: which tiers a node ended up with is decided by three separate
// conditions, and reading them back is how that is checked rather than asserted.
func (n *Instance) tiers() []invoke.Runtime { return n.runtimes }

func (n *Instance) openWorker() error {
	if !n.cfg.runs(Worker) {
		return nil
	}
	// PLM'S RECORDS ARE HELD FROM THE START, so this node's replica follows every
	// build as it lands rather than starting to catch up on the first request.
	if n.opts.artifacts == nil {
		if _, err := n.dbs.at(jobs.Records); err != nil {
			return err
		}
	}
	tiers := append([]invoke.Runtime(nil), n.opts.runtimes...)

	// THE BACKINGS ARE BUILT WHETHER OR NOT A WASM TIER USES THEM, because the
	// dispatcher reads the queue one from its own goroutine and a field
	// created later would be a field written while it is being read. They open
	// nothing until something reaches one.
	syncs, err := n.syncBacking()
	if err != nil {
		return err
	}
	n.busStreams()

	// A DECLARED TIER IS BUILT HERE; an injected one was built by whoever
	// composed this. Both end up in the same pool, and a node can have both —
	// a container backend an operator named and a wasm engine the process
	// already holds.
	if n.cfg.Container.Any() {
		disks, err := n.disksOrNil()
		if err != nil {
			return err
		}
		tier, err := containerTier(n.cfg.Container, disks, n.sharedVault())
		if err != nil {
			return err
		}
		tiers = append(tiers, tier)
	}

	// Components and scripts cold-start from the asset store with no daemon, so every
	// node serves both. An injected tier wins over a built one of the same name. A
	// site's files are the gateway's to read, not a tier's to run.
	//
	// A function's println goes to the node's log. The backings are always given and open
	// nothing until reached; without them the tier would refuse resources the node could
	// serve.
	publisher, err := n.streamPublisher()
	if err != nil {
		return err
	}

	if err := n.registerSQL(); err != nil {
		return err
	}
	servers, err := newDatabases(n)
	if err != nil {
		return err
	}
	n.closers = append(n.closers, servers)

	buckets, err := objects.New(n.store)
	if err != nil {
		return err
	}

	stores, err := cache.New(cacheStores{node: n})
	if err != nil {
		return err
	}

	bus := n.busStreams()

	// INFERENCE IS ALWAYS OFFERED, even on a node that resolves no secret:
	// a model served by something needing no credential — ollama on this
	// machine — is a legitimate resource, and refusing to start a workload
	// that reaches one would make the credential the thing that decides
	// whether inference exists.
	infer, err := n.inference()
	if err != nil {
		return err
	}

	// WHO MAY OPEN ONE INSTANCE OF A STORE THAT HOLDS MANY. It invokes the
	// validator the target named, which is why it is here and not in the
	// tier: the tier runs one workload and cannot reach another.
	admits, err := n.admissions()
	if err != nil {
		return err
	}

	n.sharedVault()

	// THE PEER THIS INSTANCE RUNS ON, for `taubyte:workload/identity`. It is
	// the host's own id rather than anything derived: what a workload is
	// told about where it is running has to be the name the swarm knows it
	// by, or a log line naming it cannot be followed to a machine.
	self := n.Host().ID().String()

	component, err := wasm.New(n.store,
		wasm.WithMode(wasm.Mode(n.cfg.Worker.Engines.Wasm)),
		wasm.WithOutput(workloadLog),
		// THE SAME REPORT THE CONTAINER TIER MAKES. The composer hands every
		// tier a grant for every kind so it can say what it cannot do; this
		// is where the wasm tier says it, instead of dropping the grant.
		wasm.WithWarn(func(format string, args ...any) { logger.Warnf(format, args...) }),
		wasm.WithNode(self),
		wasm.WithCaches(stores),
		wasm.WithStreams(publisher),
		wasm.WithBus(bus),
		wasm.WithSQL(servers),
		// KV goes through the database service; the other models do not yet.
		wasm.WithKV(n.kvBacking(servers)),
		wasm.WithDocuments(servers),
		wasm.WithVectors(servers),
		// The `sync` kind goes through the primitives service with no local fallback, which
		// would be two queues that cannot see each other.
		wasm.WithQueues(syncs),
		wasm.WithLogs(syncs),
		wasm.WithStorage(buckets),
		wasm.WithAdmission(admits),
		wasm.WithChannels(&channels{node: n}),
		wasm.WithAuthorizers(&authorizers{node: n}),
		wasm.WithSecrets(n.vault),
		wasm.WithInference(infer))
	if err != nil {
		return err
	}
	n.closers = append(n.closers, closerFunc(func() { component.Close() }))

	// KEPT, because a guard is run through it. An authorizer is a second
	// world instantiated from the same tier, and the node is the only thing
	// that holds both the tier and the worker that can resolve which
	// function to instantiate. See authorizer.go.
	n.decider = component

	// A JS function reaches what is written as a host module, the vault so
	// far, and the tier warns for each other capability a workload declares
	// rather than refusing it.
	script, err := js.New(n.store,
		js.WithMode(js.Mode(n.cfg.Worker.Engines.JS)),
		js.WithOutput(workloadLog),
		js.WithSecrets(n.vault),
		js.WithWarn(func(format string, args ...any) { logger.Warnf(format, args...) }))
	if err != nil {
		return err
	}

	tiers = appendUnnamed(tiers, component, script)

	pool, err := invoke.NewPool(invoke.WithRuntime(tiers...))
	if err != nil {
		return err
	}
	n.pool = pool
	n.runtimes = tiers
	n.closers = append(n.closers, closerFunc(func() { pool.Close() }))

	options := []worker.Option{
		worker.WithRegister(n.views),
		worker.WithPool(pool),
	}
	// WHAT A BUILT RESOURCE RUNS COMES FROM THE PLM SERVICE, unless a
	// composer injected something else. An injected one wins because it is the
	// more specific statement: somebody who handed this node an artifact
	// resolver meant that one.
	options = append(options, worker.WithArtifacts(n.Artifacts()))
	// A SECRET IS RESOLVED THE SAME WAY AN ARTIFACT IS, and by the same rule:
	// an injected resolver wins, then this node's own vault or one in the
	// fleet. Without either, a resource that names a secret is refused with
	// ErrNoSecrets rather than started without it.
	switch {
	case n.opts.secrets != nil:
		options = append(options, worker.WithSecrets(n.opts.secrets))
	case n.vault != nil:
		options = append(options, worker.WithSecrets(n.vault))
	case n.secrets != nil || n.stats != nil:
		// A worker with no wasm tier — an injected one, or a node with no
		// asset store — never built the shared vault above.
		options = append(options, worker.WithSecrets(n.sharedVault()))
	}
	if n.opts.now != nil {
		options = append(options, worker.WithClock(n.opts.now))
	}

	svc, err := worker.New(n.Host(), n.cfg.Worker, options...)
	if err != nil {
		return err
	}
	n.worker = svc
	n.closers = append(n.closers, svc)
	return nil
}

// appendUnnamed adds every tier whose name nothing already answers for.
func appendUnnamed(have []invoke.Runtime, add ...invoke.Runtime) []invoke.Runtime {
	named := make(map[string]bool, len(have))
	for _, rt := range have {
		named[rt.Name()] = true
	}

	for _, rt := range add {
		if named[rt.Name()] {
			continue
		}
		named[rt.Name()] = true
		have = append(have, rt)
	}
	return have
}

// workloadLog is where one workload's own output goes.
//
// BOTH STREAMS GO TO THE SAME PLACE, and the level is the difference: a guest
// that writes to stderr is saying something went wrong, and a node's operator
// filtering on level should see it.
func workloadLog(spec invoke.Spec) (stdout, stderr io.Writer) {
	return logLine(func(line string) { logger.Infof("%s: %s", spec.Name, line) }),
		logLine(func(line string) { logger.Warnf("%s: %s", spec.Name, line) })
}

// logLine turns a stream of bytes into whole lines, since a guest's writes follow its
// buffer flushes.
func logLine(emit func(string)) io.Writer {
	return &lineWriter{emit: emit}
}

type lineWriter struct {
	mu      sync.Mutex
	partial []byte
	emit    func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.partial = append(w.partial, p...)
	for {
		at := bytes.IndexByte(w.partial, '\n')
		if at < 0 {
			return len(p), nil
		}
		line := strings.TrimRight(string(w.partial[:at]), "\r")
		w.partial = w.partial[at+1:]
		if line != "" {
			w.emit(line)
		}
	}
}

// openPlacement builds the router that turns an address into a node, used by the
// gateway and by channel dials alike. A node without stats gets none and can only
// reach itself; the gateway refuses to start without it.
func (n *Instance) openPlacement() error {
	if n.stats == nil {
		return nil
	}

	var options []placement.Option
	if n.worker != nil {
		options = append(options, placement.WithLocal(n.worker))
	}

	workers, err := placement.New(n.Host(), n.stats, options...)
	if err != nil {
		return err
	}
	n.workers = workers
	n.closers = append(n.closers, workers)
	return nil
}

func (n *Instance) openGateway() error {
	if !n.cfg.runs(Gateway) {
		return nil
	}
	if n.workers == nil {
		return errors.New("node: a gateway places requests from stats, and this node does not run it")
	}

	source := n.views
	gatewayOptions := []gateway.Option{
		gateway.WithHosts(source),
		gateway.WithSource(source),
		gateway.WithWorkers(n.workers),
		gateway.WithLogger(logger),
	}

	// A BUS IS ATTACHED TO OVER GOSSIP, so a node with none says so by not
	// offering the option at all — the gateway then refuses a bus's name with
	// "not available here" rather than accepting the connection and never
	// delivering anything.
	if n.Messaging() != nil {
		buses, err := stream.NewBuses(n.busStreams())
		if err != nil {
			return err
		}
		gatewayOptions = append(gatewayOptions, gateway.WithBuses(buses))
	}

	// AND WHO MAY ATTACH TO ONE INSTANCE OF ONE, which is the same decision the
	// tier asks about a workload — one implementation, because two answers to
	// it would be two security policies for one config block.
	if n.worker != nil {
		admits, err := n.admissions()
		if err != nil {
			return err
		}
		gatewayOptions = append(gatewayOptions, gateway.WithAdmission(admits))
	}
	if n.opts.now != nil {
		gatewayOptions = append(gatewayOptions, gateway.WithClock(n.opts.now))
	}

	// A PROVIDED CERTIFICATE IS A SECRET OF THE DEPLOYMENT, read through the
	// resolver the worker and the proxy read through, and by the same rule:
	// an injected one wins, then this node's own vault or one in the fleet.
	switch {
	case n.opts.secrets != nil:
		gatewayOptions = append(gatewayOptions, gateway.WithSecrets(n.opts.secrets))
	case n.vault != nil:
		gatewayOptions = append(gatewayOptions, gateway.WithSecrets(n.vault))
	}
	if n.opts.terminates {
		gatewayOptions = append(gatewayOptions, gateway.WithTLS())
	}

	// A GENERATED NAME THE WILDCARD DOES NOT COVER is ordered by this node
	// and kept on the cloud's cm nodes; see cm.go.
	certs, err := n.certificates()
	if err != nil {
		return fmt.Errorf("node: gateway: %w", err)
	}
	if certs != nil {
		gatewayOptions = append(gatewayOptions, gateway.WithIssuer(certs))
	}

	// A PACKAGE HANDLER'S FILES are read from the build's artifact, as a worker
	// reads one: the same lookup, the same storage service.
	gatewayOptions = append(gatewayOptions, gateway.WithFiles(n.Artifacts(), n.store))

	svc, err := gateway.New(n.cfg.Gateway, gatewayOptions...)
	if err != nil {
		return err
	}
	n.gateway = svc
	n.closers = append(n.closers, closerFunc(func() { svc.Close() }))
	return nil
}

// Artifacts is what this node's worker looks a built resource's artifact up in: the
// one it was given, else plm's records on this node.
func (n *Instance) Artifacts() worker.Artifacts {
	if n.opts.artifacts != nil {
		return n.opts.artifacts
	}
	return n.PLMArtifacts()
}

// secretResolver is whichever resolver this node settled on, or nil.
//
// THE RULE IS THE ONE THE WORKER APPLIES: an injected resolver wins, then this
// node's own vault. It is a method rather than three copies of the switch
// because three consumers now ask the same question.
func (n *Instance) secretResolver() model.Secrets {
	switch {
	case n.opts.secrets != nil:
		return n.opts.secrets
	case n.vault != nil:
		return n.vault
	}
	return nil
}

// Start starts what was built, in the order it was built. Calling it twice, or
// after Close, is an error rather than a second listener leaking.
func (n *Instance) Start(ctx context.Context) error {
	n.startMu.Lock()
	defer n.startMu.Unlock()

	if n.started {
		return errors.New("node: already started")
	}

	// THE CONFIGURED SERVICES FIRST, then every protocol they serve advertised. What
	// they reach on the built-ins beside them they reach lazily.
	if err := n.startServices(ctx); err != nil {
		return err
	}
	n.advertise()

	var order []starter
	if n.worker != nil {
		order = append(order, n.worker)
	}

	for _, s := range order {
		if err := s.Start(ctx); err != nil {
			return err
		}
	}

	// THE BACKGROUND LOOPS LAST. They outlive the call that started them, so
	// they take a context of their own that Close cancels.
	loopCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	n.loopsStop = stop

	n.retentionDone = make(chan struct{})
	go func() {
		defer close(n.retentionDone)
		n.followRetention(loopCtx)
	}()

	// THE PROXY SWEEPS WITHOUT RAFT, unlike everything below it: every node
	// holding a deployment serves its proxies, so there is no slot to claim and
	// no leader to be.
	if n.proxy != nil {
		n.proxyDone = make(chan struct{})
		go func() {
			defer close(n.proxyDone)
			n.proxy.Run(loopCtx)
		}()
	}

	// The dispatcher runs on any node with a worker, with or without local raft: queues
	// are the primitives service's, reached through the finder when not local.
	if n.worker != nil {
		n.dispatchDone = make(chan struct{})
		go (&dispatcher{node: n}).loop(loopCtx, n.dispatchDone)

		if n.subscriptions != nil {
			n.subscribeDone = make(chan struct{})
			go n.subscribe(loopCtx, n.subscribeDone)
		}

		// The clock is the primitives service's (services/primitives/cron.go); this node only
		// supplies which crons exist and how to reach a workload.
	}

	// THE CHECKPOINTS, which is what makes a volume survive its node — and
	// which raft has nothing to do with either. A checkpoint is a row in a
	// replicated kvdb that ANY node may write; services/worker/volume's own doc records
	// the leader-only write as the bug it was, because a volume is mounted
	// wherever placement put the workload and not on a leader.
	if n.disks != nil {
		n.checkpointDone = make(chan struct{})
		go func() {
			defer close(n.checkpointDone)
			n.disks.Run(loopCtx, checkpointTick)
		}()
	}

	n.started = true
	return nil
}

// addCloser records something to close with the node.
//
// IT IS SAFE FROM ANY GOROUTINE, which the bare append was not: a cache's
// store and a database's are opened on the first request that needs one, so two
// concurrent cold starts appended to one slice at once.
func (n *Instance) addCloser(c io.Closer) {
	n.closersMu.Lock()
	n.closers = append(n.closers, c)
	n.closersMu.Unlock()
}

// Handler is what this node serves on the internet, or nil when it runs no
// gateway.
func (n *Instance) Handler() http.Handler {
	if n.gateway == nil {
		return nil
	}
	return n.gateway.Handler()
}

// TLSConfig is what a composer serving Handler over TLS answers handshakes
// with: each name's certificate is the one its gateway declares. Nil when this
// node runs no gateway.
func (n *Instance) TLSConfig() *tls.Config {
	if n.gateway == nil {
		return nil
	}
	return &tls.Config{GetCertificate: n.gateway.Certificate, MinVersion: tls.VersionTLS12}
}

// Close stops everything, in the reverse of the order it was opened. It is
// idempotent, and it closes what was built even when Start was never called.
func (n *Instance) Close() error {
	n.closeOnce.Do(func() {
		// THE BACKGROUND LOOPS GO FIRST AND ARE WAITED FOR, because everything
		// they touch is closed below them: a cycle still running while the
		// queue group is torn down is a claim against a store that is going
		// away.
		if n.loopsStop != nil {
			n.loopsStop()
			if n.proxyDone != nil {
				<-n.proxyDone
			}
			if n.dispatchDone != nil {
				<-n.dispatchDone
			}
			if n.subscribeDone != nil {
				<-n.subscribeDone
			}
			if n.checkpointDone != nil {
				<-n.checkpointDone
			}
			if n.retentionDone != nil {
				<-n.retentionDone
			}
		}
		// THE LOCATORS BEFORE THE SERVICES THEY REACH, because a stream held
		// onto another node is this node's to give back and nothing below
		// knows it exists.
		n.closeReach()

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

// builtIns is what the services this instance still builds itself add to its
// report: their names, the sockets the proxy accepts, and what the database knows
// about placement. Load is zero and said to be; stats adds the addresses.
type builtIns struct{ n *Instance }

func (b builtIns) Report(snap *statsService.Snapshot) {
	n := b.n
	snap.Serving = append(snap.Serving, n.serving()...)
	snap.Sockets = append(snap.Sockets, n.proxySockets()...)
}

// serving is every name this node answers to: the services it runs, plus what
// each of them can say about itself.
func (n *Instance) serving() []string {
	out := append([]string(nil), n.cfg.Services...)
	if n.worker != nil {
		out = append(out, n.worker.Serving()...)
	}
	// AND EVERY NAME A LISTENER ANSWERS ON. A client resolves the name to an
	// address before it asks SRV for the port, so a socket reported without
	// its name in this list is a port nobody can find the machine for.
	for _, sock := range n.proxySockets() {
		out = append(out, sock.Host)
	}
	return dedupe(out)
}

// Vault is the cloud's secrets as this node reads and writes them: through
// its own service when it runs one, otherwise through whichever nodes
// the finder names. It is made once and closed with the node.
func (n *Instance) Vault() Vault {
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	if n.shared == nil {
		n.shared = newVault(n)
		n.addCloser(n.shared)
	}
	return n.shared
}

// Services is what this node runs, for an operator asking.
func (n *Instance) Services() []string { return append([]string(nil), n.cfg.Services...) }

// Register is this node's register, or nil.
func (n *Instance) Register() registerService.Service { return n.register }

// Worker is this node's worker, or nil.
func (n *Instance) Worker() *worker.Service { return n.worker }

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
// undeclared handle, an unreachable service or an unformed group alike. A dead letter
// is read by the handle of the queue named in `dead_letter`.
func (n *Instance) QueueDepth(handle string) (ready, claimed int, ok bool) {
	if n.syncs == nil {
		return 0, 0, false
	}
	depth, err := n.syncs.Depth(context.Background(), invoke.Grant{Handle: handle})
	if err != nil {
		return 0, 0, false
	}
	return int(depth.Ready), int(depth.Claimed), true
}
