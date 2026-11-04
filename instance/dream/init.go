// Package dream runs a fleet of whole v2 nodes inside one process, v2's end-to-end
// harness. Per-service dream registrations prove a service; this proves the
// composition (a worker finding a register it does not run, a gateway routing to a
// worker elsewhere). A universe is N nodes, each on its own libp2p peer meshed on
// loopback (p2p/mesh), running the services its Config names.
package dream

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/services/build/frontend"

	"github.com/taubyte/tau/services/build/backend"

	"github.com/taubyte/tau/pkg/containers"

	auth "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/consensus"

	"github.com/taubyte/tau/instance/boot"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/secrets"

	"github.com/taubyte/tau/instance"
	"github.com/taubyte/tau/p2p/mesh"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/sectls"
	"github.com/taubyte/tau/pkg/certificate/acme"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/discovery"
	"github.com/taubyte/tau/services/worker"
)

type tierList struct {
	sync.Mutex
	list []invoke.Runtime
}

// runtimes are the tiers nodes in this process run on, process-wide since a tier is
// a live object every node shares.
var runtimes tierList

// Runtimes sets the tiers, and returns what it replaced so a test can put it
// back.
func Runtimes(rts ...invoke.Runtime) []invoke.Runtime {
	runtimes.Lock()
	defer runtimes.Unlock()
	was := runtimes.list
	runtimes.list = rts
	return was
}

type processClock struct {
	sync.Mutex
	now func() time.Time
}

// clock is what nodes in this process believe the time is, process-wide like
// runtimes; nil is the wall clock. It lets tests advance scheduled work rather than
// wait for it.
var clock processClock

// Clock sets what nodes in this process believe the time is, and returns what it
// replaced so a test can put it back. A nil clock is the wall clock.
func Clock(now func() time.Time) func() time.Time {
	clock.Lock()
	defer clock.Unlock()
	was := clock.now
	clock.now = now
	return was
}

type builtNodes struct {
	sync.Mutex
	byPeer map[string]running
}

// built is every node this process is running, by peer id, so a test can reach
// one. Nothing in production reads it.
var built = builtNodes{byPeer: map[string]running{}}

type running struct {
	node      *instance.Instance
	assets    Components
	artifacts *Built
}

// Built is what a build service would have published, staged by a test running
// none: the test writes a component into the asset store and hands the mapping
// through worker.Artifacts, the seam a cloud's build service fills, leaving the
// project's `source:` as written. The kind is dropped, as instance/build.go does, since
// a resource id is unique. Whatever is not staged is asked of the build service.
type Built struct {
	mu       sync.Mutex
	cids     map[string]string
	fallback worker.Artifacts
}

// Publish records what a build produced for each resource of one deployment,
// keyed by resource id — the shape a build service leaves behind, and the shape
// the seam is asked in.
func (b *Built) Publish(project, environment string, byResource map[string]string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cids == nil {
		b.cids = map[string]string{}
	}
	for resource, cid := range byResource {
		b.cids[builtKey(project, environment, resource)] = cid
	}
}

// Artifact is worker.Artifacts: what this resource runs. An unknown resource is a
// refusal naming it, never "".
func (b *Built) Artifact(ctx context.Context, project, environment, kind, resource string) (string, error) {
	b.mu.Lock()
	cid, ok := b.cids[builtKey(project, environment, resource)]
	fallback := b.fallback
	b.mu.Unlock()

	if ok {
		return cid, nil
	}
	if fallback == nil {
		return "", fmt.Errorf("dream: nothing was staged for %s/%s/%s", project, environment, resource)
	}
	cid, err := fallback.Artifact(ctx, project, environment, kind, resource)
	if err != nil {
		return "", fmt.Errorf("dream: nothing was staged for %s/%s/%s, and the build service has nothing either: %w", project, environment, resource, err)
	}
	return cid, nil
}

func builtKey(project, environment, resource string) string {
	return strings.Join([]string{project, environment, resource}, "\x00")
}

// Node is the node running on a peer, for a test that has its id.
func Node(peerID string) (*instance.Instance, bool) {
	built.Lock()
	defer built.Unlock()
	r, ok := built.byPeer[peerID]
	return r.node, ok
}

// Components is a node's own blocks, where a test puts a real component the node
// then reads by cid, as it reads what a build stored (see Artifacts).
type Components struct{ node peer.Node }

// Put writes content into the node's blocks and answers its cid.
func (c Components) Put(ctx context.Context, content io.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return c.node.AddFile(content)
}

// Assets is the blocks that node reads components from, exposed so a test can put a
// real component in (see Artifacts).
func Assets(peerID string) (Components, bool) {
	built.Lock()
	defer built.Unlock()
	r, ok := built.byPeer[peerID]
	return r.assets, ok
}

// Artifacts is this node's artifact seam, for a test that has to say what a
// build produced. It is the other half of Assets: the bytes go in there, and
// which resource runs them is said here.
func Artifacts(peerID string) (*Built, bool) {
	built.Lock()
	defer built.Unlock()
	r, ok := built.byPeer[peerID]
	return r.artifacts, ok
}

// Dream's raft timeouts. raft.DefaultTimeoutConfig is sized for distant nodes; in
// a loopback universe voters find each other within one 200ms discovery poll and
// would otherwise sit out a 30s bootstrap window. Production keeps the default
// (instance/boot/boot.go). Each number is a measured floor times the default's own
// ratio; discovery measured p50 0.2s, p90 0.4s, worst 3.2s.
const (
	// dreamBootstrapTimeout is the window raft.New waits for peers before deciding who
	// founds the group. A node seeing peers only after 0.8x of it joins late (slower,
	// still correct; 4s vs the worst 3.2s measured). Only a universe where every node is
	// late founds nothing, and at least one node always saw a peer within 0.8s (5x). A
	// wider window would add its full length to most tests.
	dreamBootstrapTimeout = 5 * time.Second

	// dreamHeartbeat is how long a leaderless follower waits before standing; the first
	// election after bootstrap runs it out (1x-2x randomised). Its floor is one cold
	// RPC (pkg/raft's 1s minTransportTimeout); 2s is twice that.
	dreamHeartbeat = 2 * time.Second
)

// dreamStabilize is how long a peer must be continuously present before data is
// placed on it: one liveness window, pkg/membership's minimum. A cloud waits a
// minute to avoid placement thrash on restarting peers; a universe's goroutine
// nodes never restart. A shorter window is also harder to interrupt. It took the
// settle barrier from 65s to 20s.
var dreamStabilize = discovery.Config{}.Freshness()

// dreamTimeouts is DefaultTimeoutConfig's shape at dream's scale: election twice
// heartbeat, commit and lease equal to it (hashicorp rejects a lease above the
// heartbeat). Snapshot settings are the default's.
var dreamTimeouts = raft.TimeoutConfig{
	HeartbeatTimeout:   dreamHeartbeat,
	ElectionTimeout:    2 * dreamHeartbeat,
	CommitTimeout:      dreamHeartbeat,
	LeaderLeaseTimeout: dreamHeartbeat,
	SnapshotInterval:   raft.DefaultTimeoutConfig.SnapshotInterval,
	SnapshotThreshold:  raft.DefaultTimeoutConfig.SnapshotThreshold,
}

// Config is the shape of a universe: how many nodes, and what each runs.
type Config struct {
	// Services each node runs. None named means all of them, which is what
	// node.Config already says an empty list means.
	Services []string

	// Copies is how many nodes. Zero is one.
	Copies int

	// Container is the daemon nodes run container workloads on (containers.Docker or
	// containers.Containerd). None unless asked: without one a node runs wasm and
	// static sites and refuses container workloads by name.
	Container string

	// Options are added to every node's own, such as services added with
	// node.WithService, which Services may then name.
	Options []instance.Option

	// Configure, when set, adjusts node index's config after the universe has
	// filled it — the way a fleet gives different machines different roles,
	// such as a build frontend on one node and its backend on another (a node
	// refuses to run both halves).
	Configure func(index int, c *instance.Config)

	// Secrets are cloud secrets every node that keeps secrets is initialized
	// with, beside the universe's ACME account, as `taud init -` writes a
	// sealed bundle (see secrets.Sealed).
	Secrets []*secretsv2.InitSecret

	// Local is repositories read from folders on this machine, by repository
	// key (github.com/acme/app), in place of the forge: each is a git
	// repository, cloned and confirmed at the commit a push or `tau ci run`
	// names, so a CI run needs no forge to hold the code.
	Local map[string]string
}

// removed is a directory a universe deletes when it closes.
type removed string

func (d removed) Close() error { return os.RemoveAll(string(d)) }

// Universe is a running fleet of nodes.
type Universe struct {
	mesh *mesh.Mesh
	pids []string
}

// Start runs a universe of cfg.Copies nodes, one after another, each meshed
// with the ones before it. A node that fails to start closes whatever was
// already running and is the error.
func Start(ctx context.Context, name string, cfg Config) (*Universe, error) {
	m, err := mesh.New(ctx, name)
	if err != nil {
		return nil, err
	}

	// One ACME account per universe, sealed once and written by every secrets-keeping
	// node before it starts, as `taud init` does.
	_, minted, err := acme.MintKey()
	if err != nil {
		m.Close()
		return nil, err
	}
	sealed, err := secrets.Sealed(secrets.Ref{Scope: secrets.Cloud(cm.VaultService), Name: cm.AccountSecret}, minted, time.Now())
	if err != nil {
		m.Close()
		return nil, err
	}
	account := []*secretsv2.InitSecret{sealed}

	// ONE UPLOADS DIRECTORY PER UNIVERSE, named in every node's build configuration,
	// since the frontend that takes a developer's build and the backend that builds
	// it are often two nodes.
	held, err := os.MkdirTemp("", "tau-uploads-")
	if err != nil {
		m.Close()
		return nil, err
	}
	m.OnClose(removed(held))

	u := &Universe{mesh: m}
	copies := max(cfg.Copies, 1)
	for index := range copies {
		p, err := m.Add(func(p peer.Node) error { return create(m, p, cfg, index, account, held) })
		if err != nil {
			u.Close()
			return nil, fmt.Errorf("dream: node %d: %w", index, err)
		}
		u.pids = append(u.pids, p.ID().String())
	}
	return u, nil
}

// Name is the universe's name, which is also the cloud its nodes are in.
func (u *Universe) Name() string { return u.mesh.Name() }

// Pids are the nodes' peer ids, in the order they started.
func (u *Universe) Pids() []string { return append([]string(nil), u.pids...) }

// Peers are the nodes' libp2p peers, in the order they started.
func (u *Universe) Peers() []peer.Node { return u.mesh.Peers() }

// CA is the universe's own CA, random per universe: a peer from outside
// joins with a certificate from it, issued for the universe's name.
func (u *Universe) CA() *sectls.CA { return u.mesh.CA() }

// Close stops every node, then their peers.
func (u *Universe) Close() { u.mesh.Close() }

func create(m *mesh.Mesh, p peer.Node, cfg Config, index int, account []*secretsv2.InitSecret, uploads string) error {
	ctx := m.Context()
	h := p.Host()

	runtimes.Lock()
	tiers := append([]invoke.Runtime(nil), runtimes.list...)
	runtimes.Unlock()

	clock.Lock()
	now := clock.now
	clock.Unlock()

	if len(tiers) == 0 {
		// A universe with no compute backend still composes; the tier is where it stops.
		tiers = []invoke.Runtime{absent{}}
	}

	store := Components{node: p}

	// The artifact seam, filled by staged components and otherwise by the build service
	// (Built).
	staged := &Built{}

	// A DIRECTORY PER PEER, so two nodes in one universe do not share a volume
	// by accident — which would make a test of "the bytes moved" pass without
	// anything moving.
	root, err := os.MkdirTemp("", "tau-node-"+h.ID().ShortString()+"-")
	if err != nil {
		return err
	}

	// Every node takes an ephemeral loopback DNS port: :53 needs privileges, would
	// answer for the developer's own resolver, and two nodes cannot share one.
	nodeConfig := instance.Config{
		Services:  append([]string(nil), cfg.Services...),
		Cloud:     m.Name(),
		Container: containers.Declared{Backend: cfg.Container},
	}
	nodeConfig.DNSListen = "127.0.0.1:0"
	nodeConfig.Sections = map[string]any{discovery.Descriptor.Name: discovery.Config{Stabilize: dreamStabilize}}
	// A universe is one developer's cloud, so sign-up is open.
	nodeConfig.Auth.SignUp = auth.SignUpOpen
	// A development cloud serves declared zones unproven, takes a developer's build,
	// and reads the repositories it was handed folders for.
	// A node running the public build role listens on an ephemeral loopback port too.
	nodeConfig.Sections[instance.Build] = frontend.Config{Uploads: uploads, Listen: "127.0.0.1:0"}
	nodeConfig.Sections[instance.Runner] = backend.Config{
		Uploads: uploads, Local: cfg.Local, Domains: backend.Domains{Trusted: true},
	}
	if cfg.Configure != nil {
		cfg.Configure(index, &nodeConfig)
	}

	engines, err := boot.DatabaseEngines(ctx)
	if err != nil {
		os.RemoveAll(root)
		return err
	}

	// THE UNIVERSE'S OWN DIRECTORY, so a volume in a dream is a volume: a real
	// directory, checkpointed and restored the way it is on a machine; and raft
	// timed for nodes one process apart.
	nodeConfig.Root = root
	if nodeConfig.Sections == nil {
		nodeConfig.Sections = map[string]any{}
	}
	nodeConfig.Sections[consensus.Descriptor.Name] = consensus.Config{Timeouts: dreamTimeouts, BootstrapTimeout: dreamBootstrapTimeout}

	options := []instance.Option{
		instance.WithArtifacts(staged),
		instance.WithDatabaseEngines(engines),
		instance.WithRuntimes(tiers...),
	}
	if now != nil {
		options = append(options, instance.WithClock(now))
	}
	options = append(options, cfg.Options...)

	bundle := &secretsv2.InitBundle{Secrets: cfg.Secrets}
	if nodeConfig.CM.Certificate.ACME.Enabled {
		bundle.Secrets = append(account, cfg.Secrets...)
	}
	runsSecrets := len(nodeConfig.Services) == 0 || slices.Contains(nodeConfig.Services, secrets.Descriptor.Name)
	if len(bundle.Secrets) > 0 && runsSecrets {
		var section secrets.Config
		err := nodeConfig.Section(secrets.Descriptor.Name, &section)
		if err == nil {
			err = secrets.Init(ctx, p, root, section, bundle)
		}
		if err != nil {
			os.RemoveAll(root)
			return err
		}
	}

	n, err := instance.New(p, nodeConfig, options...)
	if err != nil {
		os.RemoveAll(root)
		return err
	}
	staged.fallback = n.BuildArtifacts()
	if err := n.Start(ctx); err != nil {
		n.Close()
		os.RemoveAll(root)
		return err
	}

	id := h.ID().String()
	built.Lock()
	built.byPeer[id] = running{node: n, assets: store, artifacts: staged}
	built.Unlock()

	m.OnClose(closer{id: id, node: n, root: root})
	return nil
}

type closer struct {
	id   string
	node *instance.Instance
	root string
}

func (c closer) Close() error {
	built.Lock()
	delete(built.byPeer, c.id)
	built.Unlock()

	err := c.node.Close()
	if c.root != "" {
		os.RemoveAll(c.root)
	}
	return err
}

// absent exists only so a pool can be built at all. Its name matches no tier in
// the vocabulary, so it is never selected and the pool answers ErrNoRuntime
// naming the tier the config actually asked for.
type absent struct{}

func (absent) Name() string { return "none" }

func (absent) Start(context.Context, invoke.Spec) (invoke.Instance, error) {
	return nil, invoke.ErrNoRuntime
}
