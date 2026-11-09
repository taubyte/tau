// Package hosted is the worker as a node runs it: the tiers its workloads run on,
// built from what the node holds and the services it reaches; the backings each
// capability a workload declares is served through; and the loops that deliver a
// queue's items, a stream's messages and a volume's checkpoints.
//
// It serves the worker's protocol, so the services beside it on its node find it
// through their node's discovery and ask it what they need of it: the worker, its
// component tier, who may attach to one instance of a store, the clock's workloads
// and where a build's artifact is.
package hosted

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	logging "github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/js"
	"github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/raft"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	databaseService "github.com/taubyte/tau/services/database"
	"github.com/taubyte/tau/services/dns/zone"
	primitivesService "github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
	statsService "github.com/taubyte/tau/services/stats"
	"github.com/taubyte/tau/services/storage"
	"github.com/taubyte/tau/services/worker"
	"github.com/taubyte/tau/services/worker/admit"
	"github.com/taubyte/tau/services/worker/cache"
	"github.com/taubyte/tau/services/worker/objects"
	"github.com/taubyte/tau/services/worker/placement"
	"github.com/taubyte/tau/services/worker/primitive"
	"github.com/taubyte/tau/services/worker/stream"
	"github.com/taubyte/tau/services/worker/volume"
)

var logger = logging.Logger("tau.worker")

// Name is what a node's config runs the worker under.
var Name = worker.Descriptor.Name

// Tiers is the compute tiers whoever composed a node gave it: a tier is a live
// object, a property of the machine, and no section can declare one.
type Tiers interface {
	Runtimes() []invoke.Runtime
}

// Bus is the node's one bus: a workload's publish and a browser's attach reach the
// same topic, and a second join of it in one process would be a second mesh.
type Bus interface {
	Bus() *stream.Bus
}

// Service is the worker and everything a node builds for it.
type Service struct {
	host      service.Host
	cfg       worker.Config
	root      string
	cloud     string
	container containers.Declared

	dbs     service.Databases
	disk    databaseService.Disk
	groups  raft.Groups
	engines *database.Registry
	given   []invoke.Runtime
	// lookup is where a built resource's artifact is, when whoever composed the node
	// said; otherwise it is plm's records on this node (artifacts.go).
	lookup worker.Artifacts

	store    *storage.Reached
	views    *register.Views
	sends    *rpc.Reacher[primitive.Seam]
	database databaseService.Service
	workers  *placement.Workers
	bus      *stream.Bus
	vault    *secrets.Vault

	worker        *worker.Service
	built         *artifacts
	decider       decides
	disks         *volume.Volumes
	admit         *admit.Admissions
	subscriptions *stream.Subscriptions
	syncs         *primitive.Backing
	pool          *invoke.Pool
	runtimes      []invoke.Runtime

	// closers are everything opened, in order; Close walks them backwards. Openers on
	// the request path add to it, so it is guarded by closersMu.
	closersMu sync.Mutex
	closers   []io.Closer

	loopsStop      context.CancelFunc
	dispatchDone   chan struct{}
	subscribeDone  chan struct{}
	checkpointDone chan struct{}
	closeOnce      sync.Once
	closeErr       error
}

var (
	_ service.Service      = (*Service)(nil)
	_ statsService.Reports = (*Service)(nil)
)

// New is the worker, unstarted: Start builds it from its node.
func New() *Service { return &Service{} }

// hostConfig is what the worker reads off its node: its own section and the
// machine's container tier.
type hostConfig struct {
	worker.Config `yaml:",inline"`
	Container     containers.Declared `yaml:"container,omitempty"`
}

// ErrNoDatabases is a node that shares no databases with what it runs.
var ErrNoDatabases = errors.New("worker: its node shares no databases")

// Start builds the worker's tiers from its node and starts serving: the databases and
// consensus groups the node shares, the bus and the tiers it was given, and the
// register, storage, secrets, primitives and database services reached through their
// clients. Then it delivers what its deployments' queues and streams hold, and
// checkpoints the volumes it mounts, until it closes.
func (n *Service) Start(ctx context.Context, h service.Host) error {
	var cfg hostConfig
	if err := h.Config(&cfg); err != nil {
		return err
	}
	n.host, n.cfg, n.container = h, cfg.Config, cfg.Container
	n.root, n.cloud = h.Root(), h.Cloud()

	dbs, ok := service.Get[service.Databases](h)
	if !ok {
		return ErrNoDatabases
	}
	n.dbs = dbs
	if n.disk, ok = service.Get[databaseService.Disk](h); !ok {
		return ErrNoDatabases
	}
	n.groups, _ = service.Get[raft.Groups](h)
	if tiers, ok := service.Get[Tiers](h); ok {
		n.given = tiers.Runtimes()
	}
	n.lookup, _ = service.Get[worker.Artifacts](h)
	if bus, ok := service.Get[Bus](h); ok {
		n.bus = bus.Bus()
	}
	n.database, _ = service.Local[databaseService.Service](h, databaseService.Protocol)

	n.store = storage.StoreOn(h)
	n.views = register.ViewsOn(h)
	n.vault = secrets.VaultOn(h)
	n.closers = append(n.closers, n.store, n.views, n.vault)
	n.openPrimitivesReach()

	if err := n.open(); err != nil {
		return err
	}
	if err := n.worker.Start(ctx); err != nil {
		return err
	}
	h.Serve(worker.Protocol, n)

	// THE LOOPS OUTLIVE THE CALL THAT STARTED THEM, so they take a context of their
	// own that Close cancels.
	loopCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	n.loopsStop = stop
	n.dispatchDone = make(chan struct{})
	go (&dispatcher{node: n}).loop(loopCtx, n.dispatchDone)
	if n.subscriptions != nil {
		n.subscribeDone = make(chan struct{})
		go n.subscribe(loopCtx, n.subscribeDone)
	}
	// THE CHECKPOINTS, which is what makes a volume survive its node — and which
	// raft has nothing to do with either: a checkpoint is a row in a replicated kvdb
	// that ANY node may write, since a volume is mounted wherever placement put the
	// workload and not on a leader.
	if n.disks != nil {
		n.checkpointDone = make(chan struct{})
		go func() {
			defer close(n.checkpointDone)
			n.disks.Run(loopCtx, checkpointTick)
		}()
	}
	return nil
}

// open builds the tiers, the pool and the worker over them. Anything opened is
// recorded as it is opened, so a failure half way closes what it made.
func (n *Service) open() error {
	n.built = &artifacts{node: n}

	// PLM'S RECORDS ARE HELD FROM THE START, so this node's replica follows every
	// build as it lands rather than starting to catch up on the first request.
	if n.lookup == nil {
		if _, err := n.dbs.Database(builtRecords); err != nil {
			return err
		}
	}
	tiers := append([]invoke.Runtime(nil), n.given...)

	// THE BACKINGS ARE BUILT WHETHER OR NOT A WASM TIER USES THEM, because the
	// dispatcher reads the queue one from its own goroutine and a field created later
	// would be a field written while it is being read. They open nothing until
	// something reaches one.
	syncs, err := n.syncBacking()
	if err != nil {
		return err
	}

	// A DECLARED TIER IS BUILT HERE; an injected one was built by whoever composed the
	// node. Both end up in the same pool, and a node can have both — a container
	// backend an operator named and a wasm engine the process already holds.
	if n.container.Any() {
		disks, err := n.disksOrNil()
		if err != nil {
			return err
		}
		tier, err := containerTier(n.container, disks, n.vault)
		if err != nil {
			return err
		}
		tiers = append(tiers, tier)
	}

	publisher, err := n.streamPublisher()
	if err != nil {
		return err
	}
	if err := n.openEngines(); err != nil {
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

	// INFERENCE IS ALWAYS OFFERED, even on a node that resolves no secret: a model
	// served by something needing no credential — ollama on this machine — is a
	// legitimate resource, and refusing to start a workload that reaches one would
	// make the credential the thing that decides whether inference exists.
	infer, err := n.inference()
	if err != nil {
		return err
	}

	// WHO MAY OPEN ONE INSTANCE OF A STORE THAT HOLDS MANY. It invokes the validator
	// the target named, which is why it is here and not in the tier: the tier runs
	// one workload and cannot reach another.
	admits, err := n.admissions()
	if err != nil {
		return err
	}

	// THE PEER THIS WORKER RUNS ON, for `taubyte:workload/identity`: the name the
	// swarm knows it by, or a log line naming it cannot be followed to a machine.
	self := n.host.Peer().Host().ID().String()

	options := []wasm.Option{
		wasm.WithMode(wasm.Mode(n.cfg.Engines.Wasm)),
		wasm.WithOutput(workloadLog),
		// THE SAME REPORT THE CONTAINER TIER MAKES: the composer hands every tier a
		// grant for every kind so it can say what it cannot do.
		wasm.WithWarn(func(format string, args ...any) { logger.Warnf(format, args...) }),
		wasm.WithNode(self),
		wasm.WithCaches(stores),
		wasm.WithStreams(publisher),
		wasm.WithSQL(servers),
		// KV goes through the database service; the other models do not yet.
		wasm.WithKV(n.kvBacking(servers)),
		wasm.WithDocuments(servers),
		wasm.WithVectors(servers),
		// The `sync` kind goes through the primitives service with no local fallback,
		// which would be two queues that cannot see each other.
		wasm.WithQueues(syncs),
		wasm.WithLogs(syncs),
		wasm.WithStorage(buckets),
		wasm.WithAdmission(admits),
		wasm.WithChannels(&channels{node: n}),
		wasm.WithAuthorizers(&authorizers{node: n}),
		wasm.WithSecrets(n.vault),
		wasm.WithInference(infer),
	}
	if n.bus != nil {
		options = append(options, wasm.WithBus(n.bus))
	}
	component, err := wasm.New(n.store, options...)
	if err != nil {
		return err
	}
	n.closers = append(n.closers, closerFunc(func() { component.Close() }))

	// KEPT, because a guard is run through it: an authorizer is a second world
	// instantiated from the same tier. See authorizer.go.
	n.decider = component

	// A JS function reaches what is written as a host module, the vault so far, and
	// the tier warns for each other capability a workload declares rather than
	// refusing it.
	script, err := js.New(n.store,
		js.WithMode(js.Mode(n.cfg.Engines.JS)),
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
	n.pool, n.runtimes = pool, tiers
	n.closers = append(n.closers, closerFunc(func() { pool.Close() }))

	svc, err := worker.New(n.host.Peer().Host(), n.cfg,
		worker.WithRegister(n.views),
		worker.WithPool(pool),
		worker.WithArtifacts(n.Artifacts()),
		worker.WithSecrets(n.vault),
		worker.WithClock(n.host.Now))
	if err != nil {
		return err
	}
	n.worker = svc
	n.closers = append(n.closers, svc)

	// WHERE A REQUEST GOES is placement's, which takes the worker just made as this
	// node's own. A node with no stats places only on itself.
	if router, ok := service.Local[placement.Router](n.host, statsService.Protocol); ok {
		workers, err := placement.New(n.host.Peer().Host(), router, placement.WithLocal(svc))
		if err != nil {
			return err
		}
		n.workers = workers
		n.closers = append(n.closers, workers)
	}
	return nil
}

// Report is what the worker adds to its node's report: every deployment it serves.
func (n *Service) Report(snap *statsService.Snapshot) {
	if n.worker != nil {
		snap.Serving = append(snap.Serving, n.worker.Serving()...)
	}
}

// Worker is the worker this node runs, nil before it starts.
func (n *Service) Worker() *worker.Service { return n.worker }

// At, Resolve and Proxies are what the worker holds: a deployment's compiled project,
// what one of its resources runs, and the proxies in what it holds.
func (n *Service) At(ctx context.Context, at pkgregister.Deployment) (*specs.Project, pkgregister.Pointer, error) {
	return n.worker.At(ctx, at)
}

func (n *Service) Resolve(ctx context.Context, t worker.Target) (invoke.Spec, error) {
	return n.worker.Resolve(ctx, t)
}

func (n *Service) Proxies(ctx context.Context) []worker.Proxy { return n.worker.Proxies(ctx) }

// Guests is the worker's component tier, as a project's zone asks a resolver it names,
// or nil on a worker that runs no components.
func (n *Service) Guests() zone.Guests {
	if n.decider == nil {
		return nil
	}
	return n.decider
}

// AdmitInstance and AdmitRequest are who may attach to one instance of a store that
// holds many, and what a route's guard lets a request do: one decision for a workload
// and a browser alike.
func (n *Service) AdmitInstance(ctx context.Context, handle string, settings map[string]string, instance string) error {
	return n.admit.AdmitInstance(ctx, handle, settings, instance)
}

func (n *Service) AdmitRequest(ctx context.Context, at specs.Address, req *invoke.Request) (*invoke.Admission, error) {
	return n.admit.AdmitRequest(ctx, at, req)
}

// Artifact is where a built resource's artifact is, as Artifacts answers it.
func (n *Service) Artifact(ctx context.Context, project, environment, kind, resourceID string) (string, error) {
	return n.Artifacts().Artifact(ctx, project, environment, kind, resourceID)
}

// Schedules and Fire are the worker as the primitives clock fires through it.
func (n *Service) Schedules(ctx context.Context) []primitivesService.Schedule {
	return workloads{n}.Schedules(ctx)
}

func (n *Service) Fire(ctx context.Context, at primitivesService.Target, req *invoke.Request) (*invoke.Response, error) {
	return workloads{n}.Fire(ctx, at, req)
}

// Tiers is what this worker's pool can run.
func (n *Service) Tiers() []invoke.Runtime { return n.runtimes }

// QueueDepth is what one declared queue holds: items waiting and claimed. For
// operators and tests; it reads local state without a barrier. `ok` is false for an
// undeclared handle, an unreachable service or an unformed group alike.
func (n *Service) QueueDepth(handle string) (ready, claimed int, ok bool) {
	if n.syncs == nil {
		return 0, 0, false
	}
	depth, err := n.syncs.Depth(context.Background(), invoke.Grant{Handle: handle})
	if err != nil {
		return 0, 0, false
	}
	return int(depth.Ready), int(depth.Claimed), true
}

// Close stops the loops and waits for them, then closes everything opened, in the
// reverse of the order it was opened. It is idempotent.
func (n *Service) Close() error {
	n.closeOnce.Do(func() {
		if n.loopsStop != nil {
			n.loopsStop()
			for _, done := range []chan struct{}{n.dispatchDone, n.subscribeDone, n.checkpointDone} {
				if done != nil {
					<-done
				}
			}
		}
		if n.sends != nil {
			n.sends.Close()
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

// addCloser records something to close with the worker. It is safe from any
// goroutine: a cache's store and a database's are opened on the first request that
// needs one.
func (n *Service) addCloser(c io.Closer) {
	n.closersMu.Lock()
	n.closers = append(n.closers, c)
	n.closersMu.Unlock()
}

type closerFunc func()

func (f closerFunc) Close() error { f(); return nil }

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

// workloadLog is where one workload's own output goes. Both streams go to the same
// place, and the level is the difference: a guest that writes to stderr is saying
// something went wrong.
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
