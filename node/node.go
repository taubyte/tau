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
// The one non-obvious wire is discovery. A node broadcasts the services it runs;
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
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/node/admit"
	"github.com/taubyte/tau/node/cache"
	"github.com/taubyte/tau/node/model"
	"github.com/taubyte/tau/node/primitive"
	"github.com/taubyte/tau/node/storage"
	"github.com/taubyte/tau/node/stream"
	"github.com/taubyte/tau/node/volume"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/static"
	"github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/register"

	manet "github.com/multiformats/go-multiaddr/net"
	pkgregister "github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/frontend"

	"github.com/taubyte/tau/node/placement"
	authService "github.com/taubyte/tau/services/auth"
	certificateService "github.com/taubyte/tau/services/certificate"
	databaseService "github.com/taubyte/tau/services/database"
	"github.com/taubyte/tau/services/discovery"
	dnsService "github.com/taubyte/tau/services/dns"
	gateway "github.com/taubyte/tau/services/gateway"
	primitivesService "github.com/taubyte/tau/services/primitives"
	v2proxy "github.com/taubyte/tau/services/proxy"
	registerService "github.com/taubyte/tau/services/register"
	secretsService "github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/worker"
)

// Node is everything this machine runs.
type Node struct {
	cfg  Config
	opts options

	discovery *discovery.Service
	register  *registerService.Service
	worker    *worker.Service
	gateway   *gateway.Service
	proxy     *v2proxy.Service
	dns       *dnsService.Service

	secrets *secretsService.Service

	// certificate issues the cloud's certificates when this node runs it, and
	// certs is the client every public listener here takes them from. Both
	// nil in a cloud that issues none.
	certificate *certificateService.Service
	certs       *certificateService.Client

	// database is the service that owns this project's engines, when this node
	// runs it. A node that does not runs no database of its own — it reaches
	// one in the fleet, which is the whole reason the service exists.
	database *databaseService.Service

	// primitives is the service that owns this project's `sync` kind — its
	// queues and its logs — when this node runs it. A node that does not
	// reaches one in the fleet; see node/primitives.go for why there is no
	// third answer.
	primitives *primitivesService.Service

	// decider is this node's component tier, narrowed to the two worlds its own
	// policy drives — see decides in authorizer.go. Nil on a node with no asset
	// store, which is a node that runs no components.
	decider decides

	// vault is this node's one reader of secret values, shared by the worker
	// and the wasm tier. It holds a stream to a remote secrets service, so a
	// second would be a second stream to the same peer.
	vault *vault

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
	bus *stream.Streams

	// syncs is this node's one reach into the primitives service, shared by the
	// wasm tier's `send` and `append` and the dispatcher's claim. A second
	// would be a second stream to the same peer.
	syncs *primitive.Backing

	auth          *authService.Service
	buildBackend  *backend.Service
	buildFrontend *frontend.Service

	pool     *invoke.Pool
	runtimes []invoke.Runtime
	workers  *placement.Workers

	// closers are everything opened during construction, in the order it was
	// opened. Close walks them backwards.
	//
	// TWO OPENERS ARE ON THE REQUEST PATH — a cache's store and a database's,
	// each opened lazily under its own mutex — so the slice is shared between
	// goroutines even though most of it is filled during construction. One lock
	// for it, taken by addCloser and by Close.
	closersMu sync.Mutex
	closers   []io.Closer

	// public is what services added with WithService serve on the public
	// listener. See extend.go.
	public public

	// shared is the vault Vault hands out, made on first use.
	sharedMu sync.Mutex
	shared   *vault

	// THE THREE LOCATORS, each one rule: if this node runs the service, use it
	// directly; otherwise ask discovery which node does and dial that one. The rule
	// is p2p/rpc's — see node/locate.go — and each holds at most one stream,
	// opened on first use and re-opened when the node it names changes.
	//
	// registers is read by everything that reads configuration. sends is the
	// wasm tier's queue and log path and the dispatcher's claim. clocks is the
	// one window a minute the leader hands out, and it is the only one of the
	// three that does NOT prefer this node's own — see [Node.clockNow].
	registers *rpc.Reacher[registerSeam]
	sends     *rpc.Reacher[primitive.Seam]
	clocks    *rpc.Reacher[primitivesService.Clocks]

	// loopsStop ends the background loops this node runs, and each Done is
	// closed when that loop has stopped. All three are nil on a node that runs
	// neither a worker nor consensus.
	loopsStop      context.CancelFunc
	dispatchDone   chan struct{}
	proxyDone      chan struct{}
	checkpointDone chan struct{}

	startMu   sync.Mutex
	started   bool
	closeOnce sync.Once
	closeErr  error
}

// New builds the services this node runs. It binds nothing and starts nothing.
func New(cfg Config, opts ...Option) (*Node, error) {
	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, fmt.Errorf("node: option: %w", err)
		}
	}

	added := make([]string, 0, len(o.extra))
	for _, s := range o.extra {
		added = append(added, s.Name)
	}
	cfg, err := cfg.withDefaults(added...)
	if err != nil {
		return nil, err
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
	// THE LOCATORS FIRST, because everything opened below may be handed one and
	// because a vault built during composition builds its own. Nothing they
	// read is read yet: each closure reads a field of this node at CALL time,
	// so a service opened after this is still found through a reach made before
	// it — including the discovery view the next line opens.
	n.openReach()

	// STATS NEXT. Everything else routes on the view it maintains, including
	// the locators above, so a node that runs it must have it before anything
	// asks one of them who does what.
	if err := n.openStats(); err != nil {
		return err
	}
	if err := n.openRegister(); err != nil {
		return err
	}
	if err := n.openAuth(); err != nil {
		return err
	}
	// BEFORE THE WORKER, which resolves a secret on the request path and takes
	// the vault as a collaborator rather than looking one up per invocation.
	if err := n.openSecrets(); err != nil {
		return err
	}
	// AFTER THE SECRETS SERVICE, which keeps its keys.
	if err := n.openCertificate(); err != nil {
		return err
	}
	// BEFORE THE WORKER, for the reason the vault is: the wasm tier takes its kv
	// backing as a collaborator when the worker is composed, so a service opened
	// afterwards would be one the tier never saw.
	if err := n.openDatabase(context.Background()); err != nil {
		return err
	}
	// AND BEFORE THE WORKER FOR THE SAME REASON THE DATABASE IS: the wasm tier
	// takes its queue and log backings as collaborators when the worker is
	// composed, so a service opened afterwards would be one the tier never saw.
	if err := n.openPrimitives(); err != nil {
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
	if err := n.openProxy(); err != nil {
		return err
	}
	// AND DNS LAST OF THE EDGE SERVICES, because it publishes what the two
	// above and every other node reported — it answers from the discovery view and
	// takes nothing else.
	if err := n.openDNS(); err != nil {
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
	if !n.cfg.runs(Discovery) {
		return nil
	}
	if n.opts.pubsub == nil {
		return ErrNoPubSub
	}

	bus, err := discovery.NewBus(n.opts.pubsub, n.cfg.Cloud)
	if err != nil {
		return err
	}

	options := []discovery.Option{discovery.WithBus(bus), discovery.WithReporter(n)}
	if n.opts.now != nil {
		options = append(options, discovery.WithClock(n.opts.now))
	}

	svc, err := discovery.New(n.opts.host, n.cfg.Discovery, options...)
	if err != nil {
		return err
	}
	n.discovery = svc
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

// openSecrets starts this node's vault.
//
// ITS OWN DATABASE, not a table beside the register's. The two stores have
// different writers, different lifecycles and different trust properties —
// see services/secrets' package doc — and sharing one would have made a
// rotation a redeploy.
func (n *Node) openSecrets() error {
	if !n.cfg.runs(Secrets) {
		return nil
	}
	if n.opts.databases == nil {
		return ErrNoDatabases
	}

	// A SHARE IS THE ONE PIECE OF KEY MATERIAL A NODE CANNOT RE-DERIVE, so
	// it lives on this machine's disk, and a node with no directory of its
	// own runs no secrets rather than holding shares that die with it.
	if n.opts.root == "" {
		return secretsService.ErrNoRoot
	}

	db, err := n.opts.databases.New(logger, Secrets, rebroadcastSeconds)
	if err != nil {
		return err
	}
	n.closers = append(n.closers, closerFunc(db.Close))

	options := []secretsService.Option{
		secretsService.WithRoot(filepath.Join(n.opts.root, Secrets)),
		secretsService.WithConfig(n.cfg.Secrets),
	}
	// WHO ELSE HOLDS A SHARE IS DISCOVERY'S ANSWER on a node that has one,
	// asked each time a roster is formed; without it, the connected peers that
	// speak the protocol.
	if n.cfg.runs(Discovery) {
		options = append(options, secretsService.WithMembers(func() []libp2ppeer.ID {
			return (&fromDiscovery{node: n, name: Secrets}).Known()
		}))
	}
	if n.opts.pubsub != nil {
		options = append(options, secretsService.WithPubSub(n.opts.pubsub))
	}
	if n.opts.now != nil {
		options = append(options, secretsService.WithClock(n.opts.now))
	}

	svc, err := secretsService.New(n.opts.host, db, options...)
	if err != nil {
		return err
	}
	n.secrets = svc
	n.closers = append(n.closers, svc)
	return nil
}

// tiers is what this node's pool can run. It is here for the composition's own
// tests: which tiers a node ended up with is decided by three separate
// conditions, and reading them back is how that is checked rather than asserted.
func (n *Node) tiers() []invoke.Runtime { return n.runtimes }

func (n *Node) openWorker() error {
	if !n.cfg.runs(Worker) {
		return nil
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
	if n.cfg.Container.declared() {
		disks, err := n.disksOrNil()
		if err != nil {
			return err
		}
		tier, err := containerTier(n.cfg.Container, disks)
		if err != nil {
			return err
		}
		tiers = append(tiers, tier)
	}

	// NEITHER A SITE NOR A COMPONENT NEEDS A DAEMON OR AN OPERATOR CHOICE.
	// Both cold-start out of the asset store, so a node that can reach it
	// serves both — which means the smallest possible cloud runs a static
	// website and a wasm function, and that is most of what anybody deploys.
	//
	// AN INJECTED TIER WINS OVER A BUILT ONE, by name. Somebody who handed this
	// node a wasm runtime meant that one — a test observing what a node asks to
	// start, a composer with an engine this package does not know about — and
	// building a second under the same name is not a choice between them, it is
	// a pool that refuses to open.
	if n.opts.assets != nil {
		site, err := static.New(n.opts.assets)
		if err != nil {
			return err
		}

		// A FUNCTION'S println GOES SOMEWHERE. It is the first thing anybody
		// reaches for and it went nowhere; a node's log is not where a
		// developer would look first, but it is somewhere, and what it takes to
		// do better — per-invocation attribution — is a change to every tier's
		// interface rather than to this wiring.
		// THE BACKINGS ARE ALWAYS GIVEN, and each opens nothing until a
		// workload actually reaches one. A tier composed without them refuses
		// every resource that declares a cache, a stream, a database, a queue
		// or a secret — which on a node that has the resources for them would
		// be a refusal for no reason.
		publisher, err := n.streamBacking(stream.Stream)
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

		objects, err := storage.New(n.opts.assets, storageStores{node: n}, n.opts.now)
		if err != nil {
			return err
		}
		n.closers = append(n.closers, objects)

		stores, err := cache.New(cacheStores{node: n})
		if err != nil {
			return err
		}

		bus, err := n.busStreams()
		if err != nil {
			return err
		}

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

		// ONE VAULT FOR THE NODE, not one per consumer. It holds a stream to a
		// remote secrets service and opening a second would be a second stream
		// to the same peer; the worker below is handed this same value.
		n.vault = newVault(n)
		n.closers = append(n.closers, n.vault)

		// THE PEER THIS INSTANCE RUNS ON, for `taubyte:workload/identity`. It is
		// the host's own id rather than anything derived: what a workload is
		// told about where it is running has to be the name the swarm knows it
		// by, or a log line naming it cannot be followed to a machine.
		var self string
		if n.opts.host != nil {
			self = n.opts.host.ID().String()
		}

		component, err := wasm.New(n.opts.assets,
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
			// KV GOES THROUGH THE DATABASE SERVICE, and the other three do not
			// YET: services/database serves one cell of the grid — tau's own kv
			// over the replicated store — and the rest move as they are served.
			// A partial cutover is visible here rather than hidden behind a
			// backing that silently answers either way.
			wasm.WithKV(n.kvBacking(servers)),
			wasm.WithDocuments(servers),
			wasm.WithVectors(servers),
			// AND THE `sync` KIND GOES THROUGH THE PRIMITIVES SERVICE, both
			// types of it, which is what takes the namespace out of whichever
			// machine happened to run the code. There is no fallback to the
			// node's own queues here and node/primitives.go says why: a queue
			// answered either way is two queues that cannot see each other.
			wasm.WithQueues(syncs),
			wasm.WithLogs(syncs),
			wasm.WithStorage(objects),
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

		tiers = appendUnnamed(tiers, site, component)
	}

	if len(tiers) == 0 {
		return errors.New("node: a worker with no compute tier can run nothing; declare a container backend, give it an asset store, or inject one")
	}

	pool, err := invoke.NewPool(invoke.WithRuntime(tiers...))
	if err != nil {
		return err
	}
	n.pool = pool
	n.runtimes = tiers
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
	case n.discovery != nil:
		options = append(options, worker.WithArtifacts(&artifacts{node: n, seen: map[string]builtAt{}}))
	}
	// A SECRET IS RESOLVED THE SAME WAY AN ARTIFACT IS, and by the same rule:
	// an injected resolver wins, then this node's own vault or one in the
	// fleet. Without either, a resource that names a secret is refused with
	// ErrNoSecrets rather than started without it.
	switch {
	case n.opts.secrets != nil:
		options = append(options, worker.WithSecrets(n.opts.secrets))
	case n.vault != nil:
		options = append(options, worker.WithSecrets(n.vault))
	case n.secrets != nil || n.discovery != nil:
		// A worker with no wasm tier — an injected one, or a node with no
		// asset store — never built the shared vault above.
		n.vault = newVault(n)
		n.closers = append(n.closers, n.vault)
		options = append(options, worker.WithSecrets(n.vault))
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

// logLine turns a stream of bytes into whole lines.
//
// A LOGGER TAKES A MESSAGE AND A WRITER TAKES BYTES, and a guest writes
// whenever its buffer flushes — so without this one println can arrive as three
// log entries and two printlns as one. What is left unflushed when an instance
// goes away is lost, which is the same bargain every line-buffered logger
// makes.
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

// openPlacement builds the router that turns an address into a node.
//
// IT IS NOT THE GATEWAY'S, THOUGH THE GATEWAY WAS ONCE THE ONLY CALLER. A
// workload dialling a channel asks the same question the edge asks — which node
// runs this workload — and a node running a worker and no gateway had nothing
// to ask, so a dial could reach only the machine it started on.
//
// A NODE THAT RUNS NO STATS GETS NONE, and that is not an error here: routing
// reads the view discovery maintains, and a node without one is a node that can
// only reach itself. Whoever needs it to leave the machine says so — the
// gateway below refuses to start without it.
func (n *Node) openPlacement() error {
	if n.discovery == nil {
		return nil
	}

	var options []placement.Option
	if n.worker != nil {
		options = append(options, placement.WithLocal(n.worker))
	}

	workers, err := placement.New(n.opts.host, n.discovery, options...)
	if err != nil {
		return err
	}
	n.workers = workers
	n.closers = append(n.closers, workers)
	return nil
}

func (n *Node) openGateway() error {
	if !n.cfg.runs(Gateway) {
		return nil
	}
	if n.workers == nil {
		return errors.New("node: a gateway routes from discovery, and this node does not run one")
	}

	source := n.registerSource()
	gatewayOptions := []gateway.Option{
		gateway.WithHosts(source),
		gateway.WithSource(source),
		gateway.WithWorkers(n.workers),
		gateway.WithLogger(logger.Warnf),
	}

	// A BUS IS ATTACHED TO OVER GOSSIP, so a node with none says so by not
	// offering the option at all — the gateway then refuses a bus's name with
	// "not available here" rather than accepting the connection and never
	// delivering anything.
	if n.opts.pubsub != nil {
		backing, err := n.busStreams()
		if err != nil {
			return err
		}
		buses, err := stream.NewBuses(backing)
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

	// A GENERATED NAME THE WILDCARD DOES NOT COVER is issued by the
	// cloud's certificate nodes, once for the cloud; see certificate.go.
	certs, err := n.certificates()
	if err != nil {
		return fmt.Errorf("node: gateway: %w", err)
	}
	if certs != nil {
		gatewayOptions = append(gatewayOptions, gateway.WithIssuer(certs))
	}

	svc, err := gateway.New(n.cfg.Gateway, gatewayOptions...)
	if err != nil {
		return err
	}
	n.gateway = svc
	return nil
}

// secretResolver is whichever resolver this node settled on, or nil.
//
// THE RULE IS THE ONE THE WORKER APPLIES: an injected resolver wins, then this
// node's own vault. It is a method rather than three copies of the switch
// because three consumers now ask the same question.
func (n *Node) secretResolver() model.Secrets {
	switch {
	case n.opts.secrets != nil:
		return n.opts.secrets
	case n.vault != nil:
		return n.vault
	}
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
	if n.discovery != nil {
		order = append(order, n.discovery)
	}
	if n.register != nil {
		order = append(order, n.register)
	}
	if n.secrets != nil {
		order = append(order, n.secrets)
	}
	if n.certificate != nil {
		order = append(order, n.certificate)
	}
	// THE DATABASE BEFORE THE WORKER THAT READS THROUGH IT. It was in neither
	// this list nor any other, which is two failures at once: its listener was
	// never bound, so every peer that tried to reach it got "protocols not
	// supported" — invisible for as long as the only caller was the node's own
	// Local() — and its maintenance sweep never ran, so nothing on a composed
	// node ever reconsidered what it held.
	if n.database != nil {
		order = append(order, n.database)
	}
	// AND THE PRIMITIVES SERVICE BEFORE THE WORKER THAT SENDS THROUGH IT, for
	// the reason above it: Start binds the listener, begins forming the group
	// off the request path and starts the lifecycle pass, and a service in
	// neither this list nor any other is one whose peers get "protocols not
	// supported" and whose queues nothing ever sweeps.
	if n.primitives != nil {
		order = append(order, n.primitives)
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

	// THE SOCKET IS TAKEN HERE AND NOT AT CONSTRUCTION, for the reason the
	// gateway's is not taken at all: building a service and binding a port are
	// different failures, and a node that took :53 before it could answer
	// would be answering with an empty view.
	if err := n.listenDNS(); err != nil {
		return err
	}

	// ADDED SERVICES AFTER EVERY BUILT-IN ONE, so whatever they reach on this
	// node is answering, and closed before them because their closers are the
	// last recorded.
	if err := n.startAdded(ctx); err != nil {
		return err
	}

	// THE BACKGROUND LOOPS LAST. They outlive the call that started them, so
	// they take a context of their own that Close cancels.
	needsLoops := n.proxy != nil || n.worker != nil || n.disks != nil
	if !needsLoops {
		n.started = true
		return nil
	}

	loopCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	n.loopsStop = stop

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

	// THE DISPATCHER, on any node that runs a worker. It reads what the worker
	// holds and claims the queues those consumers name, so a node running no
	// worker has nothing to drain.
	//
	// AND IT IS NOT GATED ON RAFT ANY MORE. It used to be, and the reason it
	// gave — "a node running a worker without raft opens no queue and simply
	// finds none" — went stale with the cutover: a queue is the primitives
	// service's now, and node/primitives.go resolves it through discovery to a
	// service on ANOTHER node when this one runs none. A compute node with no
	// consensus of its own can claim, and under the old gate it never tried.
	if n.worker != nil {
		n.dispatchDone = make(chan struct{})
		go (&dispatcher{node: n}).loop(loopCtx, n.dispatchDone)

		// THE CLOCK IS NOT A LOOP HERE ANY MORE. A schedule is claimed and
		// fired by the primitives service, on the one ticker it already runs —
		// see services/primitives/cron.go. What this node contributes is which
		// crons exist and how to reach a workload, both of which
		// openPrimitives hands over.
	}

	// THE CHECKPOINTS, which is what makes a volume survive its node — and
	// which raft has nothing to do with either. A checkpoint is a row in a
	// replicated kvdb that ANY node may write; node/volume's own doc records
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
func (n *Node) addCloser(c io.Closer) {
	n.closersMu.Lock()
	n.closers = append(n.closers, c)
	n.closersMu.Unlock()
}

// Handler is what this node serves on the internet, or nil when it runs no
// gateway.
//
// A node running a service added with WithService always has one: what that
// service handles is served in front of the gateway.
func (n *Node) Handler() http.Handler {
	var gateway http.Handler
	if n.gateway != nil {
		gateway = n.gateway.Handler()
	}
	for _, s := range n.opts.extra {
		if n.cfg.runs(s.Name) {
			return n.public.front(gateway)
		}
	}
	return gateway
}

// TLSConfig is what a composer serving Handler over TLS answers handshakes
// with: each name's certificate is the one its gateway declares. Nil when this
// node runs no gateway.
func (n *Node) TLSConfig() *tls.Config {
	if n.gateway == nil {
		return nil
	}
	return &tls.Config{GetCertificate: n.gateway.Certificate, MinVersion: tls.VersionTLS12}
}

// Close stops everything, in the reverse of the order it was opened. It is
// idempotent, and it closes what was built even when Start was never called.
func (n *Node) Close() error {
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
			if n.checkpointDone != nil {
				<-n.checkpointDone
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

// Snapshot is what this node says about itself, which is what everything else
// routes on.
//
// LOAD IS ZERO AND SAID TO BE. Nothing here measures it yet, and a made-up
// number would steer traffic on fiction — zero says "as available as anything
// else", which in a fleet of nodes that all report zero is true.
func (n *Node) Snapshot(context.Context) (discovery.Snapshot, error) {
	snapshot := discovery.Snapshot{
		Ready:     true,
		Serving:   n.serving(),
		Addresses: n.addresses(),
		Sockets:   n.proxySockets(),
	}

	// WHAT THIS NODE'S OWN SERVICES SAY ABOUT PLACEMENT. The fleet revision is
	// how a node computing with a stale floor becomes visible to every other
	// node; the pending count is how much repair this node knows is outstanding
	// among the databases it holds.
	if n.database != nil {
		snapshot.FleetRevision = n.database.FleetRevision()
		snapshot.DatabasesPending = n.database.Pending()
	}

	return snapshot, nil
}

// addresses is where CLIENTS reach this node — the IPs a DNS answer is made of.
//
// THEY ARE NOT THE MULTIADDRS THE FLEET GOSSIPS OVER, and conflating the two is
// a real bug rather than a tidiness point, which is what this did: it reported
// `/ip6/…/tcp/4242` verbatim, and services/dns parsed every one of them as an
// address, failed, and answered A queries with no records at all. The proto
// field says so in its own documentation and the code did it anyway.
//
// A PEERSTORE ADDRESS IS ALSO NOT ALWAYS A CLIENT ADDRESS. Behind NAT, a mesh
// VPN or a container bridge it is routinely unreachable from the internet, so
// what is published here is the IP half of what this host believes it is
// listening on — a claim, in the same sense the load figure is, and the best
// this node can make without being told.
func (n *Node) addresses() []string {
	seen := map[string]bool{}
	var routable, loopback []string

	for _, a := range n.opts.host.Addrs() {
		ip, err := manet.ToIP(a)
		if err != nil {
			// Not an address with an IP in it — a unix socket, a circuit
			// relay. Nothing a client can be handed in an A record.
			continue
		}
		if ip.IsUnspecified() {
			// 0.0.0.0 AND :: ARE NOT ADDRESSES, they are "every address". A
			// client handed one has been told nothing.
			continue
		}

		s := ip.String()
		if seen[s] {
			continue
		}
		seen[s] = true

		if ip.IsLoopback() {
			loopback = append(loopback, s)
			continue
		}
		routable = append(routable, s)
	}

	// LOOPBACK ONLY WHEN THERE IS NOTHING ELSE, which is the rule that serves
	// both ends of the range this runs on. A node with a real address that also
	// listens on 127.0.0.1 would otherwise publish it, and some clients take
	// the first record they are given — so a share of the fleet's traffic goes
	// to whichever machine asked. A development cloud bound only to loopback
	// has no other answer, and refusing to give it would mean its own names
	// resolve to nothing on the one machine where they should.
	if len(routable) > 0 {
		return routable
	}
	return loopback
}

// serving is every name this node answers to: the services it runs, plus what
// each of them can say about itself.
func (n *Node) serving() []string {
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
// discovery names. It is made once and closed with the node.
func (n *Node) Vault() Vault {
	n.sharedMu.Lock()
	defer n.sharedMu.Unlock()
	if n.shared == nil {
		n.shared = newVault(n)
		n.addCloser(n.shared)
	}
	return n.shared
}

// Services is what this node runs, for an operator asking.
func (n *Node) Services() []string { return append([]string(nil), n.cfg.Services...) }

// Register is this node's register, or nil.
func (n *Node) Register() *registerService.Service { return n.register }

// Worker is this node's worker, or nil.
func (n *Node) Worker() *worker.Service { return n.worker }

// Secrets is this node's vault, or nil if it runs none.
func (n *Node) Secrets() *secretsService.Service { return n.secrets }

// Database is this node's database service, or nil if it runs none.
func (n *Node) Database() *databaseService.Service { return n.database }

// Primitives is this node's primitives service, or nil if it runs none.
func (n *Node) Primitives() *primitivesService.Service { return n.primitives }

// Discovery is this node's view of the fleet, or nil.
func (n *Node) Discovery() *discovery.Service { return n.discovery }

// PubSub is the gossip this node was given, for a composer that has to attach
// something to it — a stream subscriber, a test listening for what a workload
// published.
func (n *Node) PubSub() discovery.PubSub { return n.opts.pubsub }

// QueueDepth is what one declared queue holds: items waiting and items claimed.
//
// IT IS FOR AN OPERATOR AND FOR A TEST, like Loaded and Held, and it reads
// local state without a barrier — so on a follower it can lag. Nothing decides
// anything on it.
//
// THE HANDLE IS ALL A CALLER GIVES, which is what keeps it usable from outside:
// the service reads everything else about the queue out of the declaration, so
// a grant carrying only the handle is the whole of the address. `ok` is false
// for a handle this deployment declares no queue for, for a node that can reach
// no primitives service, and for one whose group has not formed — three
// different facts that a depth cannot tell apart and that nothing here decides
// on.
//
// A DEAD LETTER IS READ AS THE QUEUE IT IS, by the handle of the queue an
// author named in `dead_letter`, because that is where exhausted items land.
// A queue whose author declared none has a dead letter derived from its name,
// which nothing declares and this cannot name — see the service's Depth.Dead
// for the half that can.
func (n *Node) QueueDepth(handle string) (ready, claimed int, ok bool) {
	if n.syncs == nil {
		return 0, 0, false
	}
	depth, err := n.syncs.Depth(context.Background(), invoke.Grant{Handle: handle})
	if err != nil {
		return 0, 0, false
	}
	return int(depth.Ready), int(depth.Claimed), true
}

// source reads the register for this node's own services.
type source struct{ node *Node }

var (
	_ register.Registry = (*source)(nil)
	_ gateway.Hosts     = (*source)(nil)
)

// Resolve is what this node runs of a deployment: its destination's VIEW, so every
// consumer reading through the cache holds only what is placed where this node is.
func (s *source) Resolve(ctx context.Context, name, environment string) (pkgregister.Pointer, error) {
	c, err := s.client(ctx)
	if err != nil {
		return pkgregister.Pointer{}, err
	}
	return pkgregister.InRegion(c, s.node.destination()).Resolve(ctx, name, environment)
}

// destination is the region whose views this node reads.
func (n *Node) destination() string {
	if n.opts.destination != "" {
		return n.opts.destination
	}
	return schema.DefaultRegion
}

func (s *source) Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error) {
	c, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	return c.Object(ctx, id)
}

// ResolveFleet is what the cloud declares about itself: the replication floor
// every node has to compute placement with identically.
//
// THE LOCAL REGISTER ONLY, and that is a gap rather than a design. The document
// is published, replicated and read the way a project is, but the register's
// wire carries no call for it — so a node that runs no register of its own
// cannot learn it and falls back to the default, which is at least a number
// every such node reaches the same way. Closing it is an RPC on the register,
// not a second path to the same bytes.
func (s *source) ResolveFleet(ctx context.Context) (pkgregister.Pointer, error) {
	if s.node.register == nil {
		return pkgregister.Pointer{}, fmt.Errorf("%w: this node runs no register to read the fleet from", ErrNoRegister)
	}
	return s.node.register.Local().ResolveFleet(ctx)
}

func (s *source) ResolveHost(ctx context.Context, fqdn string) (string, string, error) {
	c, err := s.client(ctx)
	if err != nil {
		return "", "", err
	}
	return c.ResolveHost(ctx, fqdn)
}
