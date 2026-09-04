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
	"sort"
	"strings"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"

	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	dreamv2 "github.com/taubyte/tau/dream/v2"
	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/assets"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/kvdb"
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

// built is every node this process is running, by peer id, so a test can reach
// one. Nothing in production reads it.
var built struct {
	sync.Mutex
	byPeer map[string]*node.Node
}

// Node is the node running on a peer, for a test that has its id.
func Node(peerID string) (*node.Node, bool) {
	built.Lock()
	defer built.Unlock()
	n, ok := built.byPeer[peerID]
	return n, ok
}

func init() {
	built.byPeer = map[string]*node.Node{}
	if err := dreamv2.Register(Protocol, create); err != nil {
		panic(err)
	}
}

func create(ctx context.Context, h host.Host, p peer.Node, u *dream.Universe, cfg *iface.ServiceConfig) (io.Closer, error) {
	runtimes.Lock()
	tiers := append([]invoke.Runtime(nil), runtimes.list...)
	runtimes.Unlock()

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

	n, err := node.New(
		node.Config{Services: servicesIn(cfg), Cloud: u.Name()},
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
		node.WithRuntimes(tiers...),
	)
	if err != nil {
		return nil, err
	}
	if err := n.Start(ctx); err != nil {
		n.Close()
		return nil, err
	}

	id := h.ID().String()
	built.Lock()
	built.byPeer[id] = n
	built.Unlock()

	return closer{id: id, node: n}, nil
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

// Services renders a services list into the config bag create reads it from.
// Merge it with whatever else the universe config carries.
//
//	Others: nodeDream.Services(node.Register, node.Stats)
func Services(names ...string) map[string]int {
	out := map[string]int{}
	for _, name := range names {
		out[servicePrefix+name] = 1
	}
	return out
}

type closer struct {
	id   string
	node *node.Node
}

func (c closer) Close() error {
	built.Lock()
	delete(built.byPeer, c.id)
	built.Unlock()
	return c.node.Close()
}

// absent exists only so a pool can be built at all. Its name matches no tier in
// the vocabulary, so it is never selected and the pool answers ErrNoRuntime
// naming the tier the config actually asked for.
type absent struct{}

func (absent) Name() string { return "none" }

func (absent) Start(context.Context, invoke.Spec) (invoke.Instance, error) {
	return nil, invoke.ErrNoRuntime
}
