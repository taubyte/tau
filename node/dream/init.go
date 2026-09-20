// Package dream runs a whole v2 node inside a dream universe.
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
// So this registers ONE service that is a whole node, and a universe of them is
// a fleet. Which services each node runs comes from the universe's own config,
// so a test writes the shape it wants to prove.
package dream

import (
	"context"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"

	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	dreamv2 "github.com/taubyte/tau/dream/v2"
	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/assets"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/placement"
	"github.com/taubyte/tau/pkg/raft"
)

// Protocol is what a universe calls a v2 node.
const Protocol = "tau"

// servicePrefix marks an entry in the universe config's `Others` bag as one of
// the services this node should run: `svc:register`, `svc:worker`.
//
// IT IS `Others` AND NOT `Root`, which is the node's data directory — dream
// hands it to tauConfig.NewNode, so putting anything else there would move
// where a node keeps its state. `Others` is the per-service bag dream already
// carries and `copies` is the only key anything else reads.
const servicePrefix = "svc:"

// regionPrefix marks the universe config entry naming the region a node is in:
// `region:eu`.
//
// A KEY AND NOT A VALUE, because `Others` is a map[string]int and a region is a
// label rather than a number — the same reason the service list is spelled as
// keys. A node with no such entry is in no region, which is what a single-region
// universe wants and what every test that is not about placement gets.
const regionPrefix = "region:"

// runtimes are the tiers nodes in this process run on.
//
// A COMPUTE TIER IS A LIVE OBJECT and dream's registry hands a service a
// universe and a yaml-shaped config, which is neither. A composer that has one
// sets it before starting a universe; a test that wants to see what a node
// asked to start supplies a recorder.
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
// object and dream's registry hands a service a universe and a yaml-shaped
// config, which is neither. Unset means the wall clock, which is what a machine
// has.
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
var built struct {
	sync.Mutex
	byPeer map[string]running
}

type running struct {
	node   *node.Node
	assets *assets.Store
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
// the artifact seam; in a universe with no build service, a resource naming its
// component directly needs the bytes to already be in the store.
func Assets(peerID string) (*assets.Store, bool) {
	built.Lock()
	defer built.Unlock()
	r, ok := built.byPeer[peerID]
	return r.assets, ok
}

func init() {
	built.byPeer = map[string]running{}
	if err := dreamv2.Register(Protocol, create); err != nil {
		panic(err)
	}
}

func create(ctx context.Context, h host.Host, p peer.Node, u *dream.Universe, cfg *iface.ServiceConfig) (io.Closer, error) {
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
		return nil, err
	}

	// A DIRECTORY PER PEER, under the universe's own temporary root, so two
	// nodes in one universe do not share a volume by accident — which would
	// make a test of "the bytes moved" pass without anything moving.
	root, err := os.MkdirTemp("", "tau-node-"+h.ID().ShortString()+"-")
	if err != nil {
		return nil, err
	}

	// A UNIVERSE CANNOT HAVE :53, AND SHOULD NOT WANT IT. The default is the
	// port a resolver dials by convention, which needs root or
	// CAP_NET_BIND_SERVICE — and even where a test has them, a universe
	// answering on the machine's own resolver port would be answering for the
	// developer running it. So every node here takes an ephemeral loopback
	// port, which is also what makes a FLEET of them possible: two nodes
	// cannot share one port, and :0 is how dream allocates every other one.
	nodeConfig := node.Config{Services: servicesIn(cfg), Cloud: u.Name(), Region: regionIn(cfg, h.ID().String())}
	nodeConfig.DNSListen = "127.0.0.1:0"

	options := []node.Option{
		node.WithHost(h),
		node.WithPubSub(p.Messaging()),
		node.WithDatabases(kvdb.New(p)),
		// THE SAME RAFT A MACHINE OPENS, over the universe's own peer. dream is
		// not a machine but it does have a peer.Node, which is the whole of
		// what raft.New wants — so parity here is the real thing rather than a
		// stand-in, and a build queue in a universe replicates the way it does
		// in a fleet.
		node.WithRaft(func(namespace string) (raft.Cluster, error) {
			return raft.New(p, namespace)
		}),
		node.WithAssets(store),
		// THE UNIVERSE'S OWN DIRECTORY, so a volume in a dream is a volume: a
		// real directory, checkpointed and restored the way it is on a machine.
		node.WithRoot(root),
		node.WithRuntimes(tiers...),
	}
	if now != nil {
		options = append(options, node.WithClock(now))
	}

	n, err := node.New(nodeConfig, options...)
	if err != nil {
		return nil, err
	}
	if err := n.Start(ctx); err != nil {
		n.Close()
		return nil, err
	}

	id := h.ID().String()
	built.Lock()
	built.byPeer[id] = running{node: n, assets: store}
	built.Unlock()

	return closer{id: id, node: n, root: root}, nil
}

// servicesIn reads the services a node runs out of the universe's config. None
// named means all of them, which is what node.Config already says an empty list
// means.
func servicesIn(cfg *iface.ServiceConfig) []string {
	if cfg == nil {
		return nil
	}

	var out []string
	for key := range cfg.Others {
		if name, ok := strings.CutPrefix(key, servicePrefix); ok && name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// regionIn is the region THIS node is in, out of the regions the universe
// declares.
//
// A UNIVERSE EXPRESSES ONE NODE SHAPE. `copies: 4` is four nodes from one
// config, so a fleet spread over two regions cannot be written as two configs —
// the universe declares the regions and each node takes one.
//
// WHICH ONE IS HRW OVER THE PEER ID, the same rendezvous every placement in this
// system uses. It spreads evenly, it needs no copy index that dream does not
// carry, and it is reproducible: the same universe puts the same peer in the
// same region every run, so a test that failed can be read.
//
// No region declared is a node in no region, which is what a single-region
// universe wants and what every test not about placement gets.
func regionIn(cfg *iface.ServiceConfig, peerID string) string {
	if cfg == nil {
		return ""
	}

	var declared []placement.Member
	for key := range cfg.Others {
		if name, ok := strings.CutPrefix(key, regionPrefix); ok && name != "" {
			declared = append(declared, placement.Member{ID: name, Accepts: true})
		}
	}
	if len(declared) == 0 {
		return ""
	}
	return placement.Rank(peerID, declared)[0].ID
}

// Bag is a universe config's per-service map, which is where a node reads what
// it runs and where it is.
type Bag map[string]int

// In spreads this universe's nodes across the named regions. Every node reads
// the same list and takes one of them — see regionIn.
//
//	Others: nodeDream.Services(node.Register, node.Stats).In("eu", "us")
func (b Bag) In(regions ...string) Bag {
	for _, region := range regions {
		if region != "" {
			b[regionPrefix+region] = 1
		}
	}
	return b
}

// Services renders a services list into the config bag create reads it from.
// Merge it with whatever else the universe config carries.
//
//	Others: nodeDream.Services(node.Register, node.Stats)
func Services(names ...string) Bag {
	out := Bag{}
	for _, name := range names {
		out[servicePrefix+name] = 1
	}
	return out
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
