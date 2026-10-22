// Package dream runs a fleet of whole v2 nodes inside one process: v2's
// end-to-end test harness.
//
// # Why the node and not each service
//
// Each v2 service already has its own dream registration, and those prove the
// service. What they cannot prove is the composition: that a worker finds a
// register it does not run, that a gateway routes to a worker on another node,
// that what a node broadcasts is what the next node looks for. Those are
// properties of how the pieces are wired, and the only way to test wiring is to
// wire it.
//
// So a universe here is N whole nodes, each on its own libp2p peer, meshed on
// loopback (p2p/mesh). Which services each node runs comes from the universe's
// Config, so a test writes the shape it wants to prove.
//
// IT USED TO BE A SERVICE REGISTERED WITH v1's dream, and that is why it is not
// any more: dream's universe is built around v1's service registry and clients,
// so every v2 test binary that wanted a mesh linked all of v1.
package dream

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	auth "github.com/taubyte/tau/services/auth"

	"github.com/taubyte/tau/node/boot"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/secrets"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/p2p/mesh"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/sectls"
	"github.com/taubyte/tau/pkg/assets"
	"github.com/taubyte/tau/pkg/certificate/acme"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/discovery"
)

// runtimes are the tiers nodes in this process run on.
//
// A COMPUTE TIER IS A LIVE OBJECT shared by every node a test starts, so it is
// set for the process rather than per universe. A composer that has one sets it
// before starting a universe; a test that wants to see what a node asked to
// start supplies a recorder.
var runtimes struct {
	sync.Mutex
	list []invoke.Runtime
}

// Runtimes sets the tiers, and returns what it replaced so a test can put it
// back.
func Runtimes(rts ...invoke.Runtime) []invoke.Runtime {
	runtimes.Lock()
	defer runtimes.Unlock()
	was := runtimes.list
	runtimes.list = rts
	return was
}

// clock is what nodes in this process believe the time is.
//
// IT IS SET THE WAY runtimes IS, and for the same reason: a clock is a live
// object shared by every node. Unset means the wall clock, which is what a
// machine has.
//
// WHY A UNIVERSE WANTS ONE: anything a node does on a schedule — a cron slot, a
// deploy budget expiring, a volume checkpoint coming due — otherwise has to be
// WAITED FOR at whatever pace the wall clock runs, and a test that waits for a
// cadence fails on a busy machine while passing on an idle one. With a clock it
// is advanced instead, and what is left to wait for is the node's own answer.
var clock struct {
	sync.Mutex
	now func() time.Time
}

// Clock sets what nodes in this process believe the time is, and returns what it
// replaced so a test can put it back. A nil clock is the wall clock.
func Clock(now func() time.Time) func() time.Time {
	clock.Lock()
	defer clock.Unlock()
	was := clock.now
	clock.now = now
	return was
}

// built is every node this process is running, by peer id, so a test can reach
// one. Nothing in production reads it.
var built = struct {
	sync.Mutex
	byPeer map[string]running
}{byPeer: map[string]running{}}

type running struct {
	node      *node.Node
	assets    *assets.Store
	artifacts *Built
}

// Built is what a build service would have published, for a universe that runs
// none.
//
// A UNIVERSE HAS NO BUILD SERVICE AND THIS IS NOT A WAY TO GIVE IT ONE. Building
// a corpus project needs a git server and a toolchain, so what each resource runs
// is a component this tree already ships — which is a DEPLOYMENT fact, and the
// artifact seam is precisely where a deployment fact belongs. In a cloud the
// build service writes the component into the asset store and its cid reaches the
// worker through worker.Artifacts; here the TEST writes it into the asset store
// and hands the same mapping through the same seam. The project keeps the
// `source:` its author wrote.
//
// IT REPLACED A REWRITE OF THE COMPILED DOCUMENT, and that is the whole reason it
// exists. The corpus used to overwrite every resource's source with
// `{kind: component, component: <cid>}` before publishing, which made `component`
// mean "this already IS an artifact, skip the build" — a meaning the vocabulary
// never gave it (`source.component` is a `Ref("component")`, so it carries a
// configuration document's id) and which only worked because the worker handed
// that id straight to the asset store. That was a bug, and the corpus was
// incapable of catching it: the one source kind an author can write there was
// only ever exercised with something that already was an artifact. See
// services/worker/artifact_test.go.
//
// THE KIND IS DROPPED, exactly as the real resolver drops it (node/build.go): a
// resource id is already unique, so a component asked about under kind
// `component` and a workload asked about under its own kind are one lookup.
type Built struct {
	mu   sync.Mutex
	cids map[string]string
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

// Artifact is worker.Artifacts: what this resource currently runs.
//
// AN UNKNOWN RESOURCE IS A REFUSAL NAMING IT, never an empty string. A tier
// handed "" asks the asset store for nothing and fails somewhere else, which is
// how a project the test forgot to stage would read as a broken runtime.
func (b *Built) Artifact(_ context.Context, project, environment, _, resource string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	cid, ok := b.cids[builtKey(project, environment, resource)]
	if !ok {
		return "", fmt.Errorf("dream: nothing was staged for %s/%s/%s", project, environment, resource)
	}
	return cid, nil
}

func builtKey(project, environment, resource string) string {
	return strings.Join([]string{project, environment, resource}, "\x00")
}

// Node is the node running on a peer, for a test that has its id.
func Node(peerID string) (*node.Node, bool) {
	built.Lock()
	defer built.Unlock()
	r, ok := built.byPeer[peerID]
	return r.node, ok
}

// Assets is the content store that node reads a component out of.
//
// IT IS HERE SO A TEST CAN PUT ONE IN. Everything else a node runs is reachable
// through the node; the asset store is built for it rather than handed to it,
// and a test that wants a REAL component to run has to be able to address one.
// In a cloud the build service writes here and a cid reaches the worker through
// the artifact seam; in a universe the test writes here, and the cid reaches the
// worker through the same seam — see Artifacts.
func Assets(peerID string) (*assets.Store, bool) {
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

// A DREAM FLEET IS ONE PROCESS ON LOOPBACK, AND raft.DefaultTimeoutConfig IS
// NOT. That default is sized for nodes on separate continents, and in a
// universe most of it is dead wait: a group's voters find each other inside a
// single 200ms discovery poll (BootstrapCheckInterval, pkg/raft/vars.go:7) and
// then sit out a 30s bootstrap window before anyone is allowed to found the
// group. Production keeps the default — see node/boot/boot.go, which passes no
// timeouts at all. Only dream tunes.
//
// TUNING DREAM'S RAFT IS NOT NEW HERE: v1's dream has always done it
// (dream/common/raft.go, 50ms timeouts and a 1s window) and so does the CLI's
// dev cloud (cli/node/start.go, a 5s window and a 1s heartbeat). Both are sized
// for a group that bootstraps alone — v1's own comment says as much — while a
// v2 universe forms real multi-voter groups, so this is the same idea with the
// founding window taken seriously.
//
// EVERY NUMBER BELOW IS A MEASURED FLOOR TIMES THE DEFAULT'S OWN RATIO, never a
// value that made a test pass. The measurement is 20 group openings across
// three universes on the build server at load 7, with raft-cluster logging on:
// discovery (the cluster's own start to its first sighting of a peer) p50
// 0.202s, p90 0.401s, max 0.802s, and a worst case of 3.2s seen in a run of the
// whole package.
const (
	// dreamBootstrapTimeout is the window raft.New waits for peers before it
	// decides who founds the group (pkg/raft/cluster.go:193-221).
	//
	// IT IS TWO MARGINS ON TWO DIFFERENT QUANTITIES, and only one of them can
	// wedge a group:
	//
	//   - A node whose peers are ALL first seen after 0.8*timeout
	//     (bootstrapThreshold, cluster.go:81, applied at :222) is a late
	//     joiner: it is left out of the founder set and asks to be added as a
	//     voter once the group exists (cluster.go:224-228). Slower, still
	//     correct. 4s against the worst per-node discovery measured here (3.2s)
	//     is 1.25x — thin, and deliberately so; see below.
	//   - A universe where EVERY node is late founds nothing at all, and that
	//     is the one that hangs. It needs the whole mesh to be late rather than
	//     one node, and in every universe measured at least one node had seen a
	//     peer inside 0.80s, so the margin on the quantity that can actually
	//     wedge a group is 5x.
	//
	// THE THIN MARGIN IS THE CHEAP ONE TO LOSE, which is why the window is not
	// wider. Nothing leaves this window early unless there is already a leader
	// to join, so a fresh universe pays it in full on every group and every
	// second added here is a second added to most tests in the package — while
	// the cost of crossing the thin margin is one node joining a beat late.
	dreamBootstrapTimeout = 5 * time.Second

	// dreamHeartbeat is what a follower waits without a leader before standing
	// for election, and it is the timeout the FIRST election after a bootstrap
	// has to run out (hashicorp randomizes it over 1x-2x).
	//
	// ITS FLOOR IS ONE COLD RPC, and pkg/raft already names that floor:
	// minTransportTimeout is 1s (cluster.go:390) for "a dial + libp2p stream
	// negotiation on a cold peer", and createTransport (:393) holds the I/O
	// deadline at it however low the heartbeat goes. Below that floor a
	// follower would be calling elections against RPCs still in flight. 2s is
	// twice it, and the first election after a bootstrap has been measured at
	// 2-4s, which is the 1x-2x randomization and nothing else.
	dreamHeartbeat = 2 * time.Second
)

// dreamStabilize is how long a peer must be continuously present before this
// universe will place data on it. It is ONE LIVENESS WINDOW, which is the
// shortest pkg/membership permits and, measurably, the only term in the settle
// barrier that is not already sub-second.
//
// ★ WHY A UNIVERSE MAY GO THIS LOW AND A CLOUD MAY NOT. Stabilize buys off
// placement thrash: a peer that restarts and is back in four seconds must not
// have its share of the fleet's bytes moved out and back, so a cloud refuses to
// move on anything shorter than a minute (services/discovery/config.go). A universe
// is one process on loopback whose nodes are goroutines that do not restart, so
// there is no thrash to buy and the minute is a minute of nothing happening.
//
// ★ SHORTENING IT IS NOT A WIDER BUDGET, IT IS A NARROWER PROMISE. A peer
// enters the placement set after being CONTINUOUSLY live for this long, so the
// window is also the length of the run a starved node has to complete without
// missing three consecutive beats — 3 beats at 15s rather than 12 at a minute.
// A shorter window is therefore strictly harder to interrupt, which is why this
// is the direction the fix goes rather than a longer wait for it.
//
// MEASURED on an idle build server, five runs: node/dream's settle barrier
// (project_test.go's deployFleetOf) sat at 1m5.001s, of which 60s was this
// window and 5.00s the first peer report heard, while the register's CRDT and
// the fleet document were both there at 1.0s. The barrier's budget is 120s, so
// a minute-long window left 55s for everything else and a loaded box spent it.
// One liveness window puts the same barrier at 20.000s.
var dreamStabilize = discovery.Config{}.Freshness()

// dreamTimeouts is DefaultTimeoutConfig's shape at dream's scale: election is
// twice heartbeat and commit and lease equal it, exactly as the default has
// 30s/15s/15s/15s. The lease is not a free choice either — hashicorp rejects a
// LeaderLeaseTimeout larger than the heartbeat outright (raft/config.go:369),
// so equal to it is the ceiling.
//
// THE SNAPSHOT NUMBERS ARE THE DEFAULT'S, UNTOUCHED. No universe lives ten
// minutes or writes 32768 entries, so a smaller interval or threshold could
// only add snapshot work to tests that never reach one.
var dreamTimeouts = raft.TimeoutConfig{
	HeartbeatTimeout:   dreamHeartbeat,
	ElectionTimeout:    2 * dreamHeartbeat,
	CommitTimeout:      dreamHeartbeat,
	LeaderLeaseTimeout: dreamHeartbeat,
	SnapshotInterval:   raft.DefaultTimeoutConfig.SnapshotInterval,
	SnapshotThreshold:  raft.DefaultTimeoutConfig.SnapshotThreshold,
}

// Config is the shape of a universe: how many nodes, and what each runs and
// where it is.
type Config struct {
	// Services each node runs. None named means all of them, which is what
	// node.Config already says an empty list means.
	Services []string

	// Copies is how many nodes. Zero is one.
	Copies int

	// Container is the daemon the nodes run container workloads on —
	// node.BackendDocker or node.BackendContainerd — the way a machine's
	// config names one.
	//
	// A UNIVERSE HAS NONE UNLESS IT ASKS, because a daemon is a property of
	// the machine running the test and most of them have none. A node without
	// one runs wasm and static sites and refuses a container workload by name;
	// a universe that names one builds the same tier a machine's config builds
	// (node/container.go).
	Container string

	// Options are added to every node's own, such as services added with
	// node.WithService, which Services may then name.
	Options []node.Option

	// Configure, when set, adjusts node index's config after the universe has
	// filled it — the way a fleet gives different machines different roles,
	// such as a build frontend on one node and its backend on another (a node
	// refuses to run both halves).
	Configure func(index int, c *node.Config)
}

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

	// ONE ACME ACCOUNT PER UNIVERSE, as an operator configures one per cloud,
	// sealed once and written by every node that keeps secrets before it
	// starts, the way `taud init` does on a machine.
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
	account := &secretsv2.InitBundle{Secrets: []*secretsv2.InitSecret{sealed}}

	u := &Universe{mesh: m}
	copies := max(cfg.Copies, 1)
	for index := range copies {
		p, err := m.Add(func(p peer.Node) error { return create(m, p, cfg, index, account) })
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

func create(m *mesh.Mesh, p peer.Node, cfg Config, index int, account *secretsv2.InitBundle) error {
	ctx := m.Context()
	h := p.Peer()

	runtimes.Lock()
	tiers := append([]invoke.Runtime(nil), runtimes.list...)
	runtimes.Unlock()

	clock.Lock()
	now := clock.now
	clock.Unlock()

	if len(tiers) == 0 {
		// A universe with no compute backend still composes: the lookup is
		// what a node proves, and the tier is where a process without a
		// container daemon stops. See services/worker/dream for the same
		// boundary stated at more length.
		tiers = []invoke.Runtime{absent{}}
	}

	store, err := assets.New(p)
	if err != nil {
		return err
	}

	// THE SEAM A CLOUD FILLS WITH ITS BUILD SERVICE. A universe runs none, so it
	// is filled by whoever staged the components — see Built.
	staged := &Built{}

	// A DIRECTORY PER PEER, so two nodes in one universe do not share a volume
	// by accident — which would make a test of "the bytes moved" pass without
	// anything moving.
	root, err := os.MkdirTemp("", "tau-node-"+h.ID().ShortString()+"-")
	if err != nil {
		return err
	}

	// A UNIVERSE CANNOT HAVE :53, AND SHOULD NOT WANT IT. The default is the
	// port a resolver dials by convention, which needs root or
	// CAP_NET_BIND_SERVICE — and even where a test has them, a universe
	// answering on the machine's own resolver port would be answering for the
	// developer running it. So every node here takes an ephemeral loopback
	// port, which is also what makes a FLEET of them possible: two nodes
	// cannot share one port, and :0 is how every other one is allocated.
	nodeConfig := node.Config{
		Services:  append([]string(nil), cfg.Services...),
		Cloud:     m.Name(),
		Container: node.ContainerConfig{Backend: cfg.Container},
	}
	nodeConfig.DNSListen = "127.0.0.1:0"
	nodeConfig.Discovery.Stabilize = dreamStabilize
	// A UNIVERSE IS ONE DEVELOPER'S CLOUD, so whoever its forges vouch for may
	// use it, as before it had users; a test that wants invites says so.
	nodeConfig.Auth.SignUp = auth.SignUpOpen
	// A UNIVERSE IS A DEVELOPMENT CLOUD, so the zones its projects declare are
	// served unproven, as v1's dev mode did; a test that proves them says so.
	nodeConfig.CM.Domains.Trusted = true
	if cfg.Configure != nil {
		cfg.Configure(index, &nodeConfig)
	}

	engines, err := boot.DatabaseEngines(ctx)
	if err != nil {
		os.RemoveAll(root)
		return err
	}

	options := []node.Option{
		node.WithHost(h),
		node.WithPubSub(p.Messaging()),
		node.WithDatabases(kvdb.New(p)),
		// THE SAME RAFT A MACHINE OPENS, over the universe's own peer. A
		// universe is not a machine but it does have a peer.Node, which is the
		// whole of what raft.New wants — so parity here is the real thing
		// rather than a stand-in, and a build queue in a universe replicates
		// the way it does in a fleet.
		node.WithRaft(func(namespace string) (raft.Cluster, error) {
			// SNAPSHOTS GO UNDER THIS NODE'S OWN ROOT. The default is a
			// machine-wide /tmp path keyed by namespace alone, so two nodes
			// that open the same group name share one snapshot store and
			// write each other's state — latent while there was one group
			// per node, and multiplied by a group per database.
			return raft.New(p, namespace,
				raft.WithSnapshotRoot(filepath.Join(root, "raft")),
				raft.WithTimeouts(dreamTimeouts),
				raft.WithBootstrapTimeout(dreamBootstrapTimeout),
			)
		}),
		node.WithAssets(store),
		node.WithArtifacts(staged),
		// THE UNIVERSE'S OWN DIRECTORY, so a volume in a dream is a volume: a
		// real directory, checkpointed and restored the way it is on a machine.
		node.WithRoot(root),
		node.WithDatabaseEngines(engines),
		node.WithRuntimes(tiers...),
	}
	if now != nil {
		options = append(options, node.WithClock(now))
	}
	options = append(options, cfg.Options...)

	if nodeConfig.CM.Certificate.ACME.Enabled {
		err := node.InitSecrets(ctx, nodeConfig, root, h, kvdb.New(p), account)
		if err != nil && !errors.Is(err, node.ErrNoSecretsHere) {
			os.RemoveAll(root)
			return err
		}
	}

	n, err := node.New(nodeConfig, options...)
	if err != nil {
		os.RemoveAll(root)
		return err
	}
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
	node *node.Node
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
