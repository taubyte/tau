package swarm_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/libp2p/go-libp2p/p2p/protocol/identify"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/database/engines/sql/distributed"
	"github.com/taubyte/tau/pkg/database/engines/sql/distributed/swarm"
)

// node is one host with its own gossip router and its own engine over a mesh.
type node struct {
	host   host.Host
	gossip *pubsub.PubSub
	engine *distributed.Engine
	mesh   *swarm.Mesh
}

// swarmOf is n nodes on one mocknet, each gossiping to every other before a mesh is
// built. A gossipsub stream is opened only at start or when identify reports gossip,
// and identify can answer from a stale snapshot, with the corrective push limited per
// subnet (all mocknet peers share one). So hosts identify each other as gossiping
// before the swarm connects.
func swarmOf(t *testing.T, n int) []node {
	t.Helper()
	ctx := t.Context()
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	out := make([]node, n)
	for i := range out {
		h, err := mn.GenPeer()
		require.NoError(t, err)
		ps, err := pubsub.NewGossipSub(ctx, h)
		require.NoError(t, err)
		out[i] = node{host: h, gossip: ps}
	}
	require.NoError(t, mn.LinkAll())
	for _, to := range out[1:] {
		identifiedAsGossiping(t, mn, out[0].host, to.host)
	}
	require.NoError(t, mn.ConnectAllButSelf())

	for i, from := range out {
		for j, to := range out {
			if i == j {
				continue
			}
			require.Eventually(t, func() bool {
				return slices.Contains(from.gossip.ListPeers(""), to.host.ID())
			}, 30*time.Second, 10*time.Millisecond, "node %d never opened a stream to node %d", i, j)
		}
	}

	for i := range out {
		m, err := swarm.New(ctx, out[i].host, out[i].gossip)
		require.NoError(t, err)
		t.Cleanup(func() { m.Close() })
		e, err := distributed.New(distributed.InDir(t.TempDir()), distributed.WithSync(m.Sync))
		require.NoError(t, err)
		out[i].mesh, out[i].engine = m, e
	}
	return out
}

// identifiedAsGossiping connects a and b, again until each has identified the
// other as gossiping.
func identifiedAsGossiping(t *testing.T, mn mocknet.Mocknet, a, b host.Host) {
	t.Helper()
	gossips := func(h host.Host, of peer.ID) bool {
		protos, _ := h.Peerstore().GetProtocols(of)
		return slices.ContainsFunc(protos, func(p protocol.ID) bool { return strings.HasPrefix(string(p), "/meshsub/") })
	}
	identified := func(h host.Host, of peer.ID) {
		ids := h.(interface{ IDService() identify.IDService }).IDService()
		for _, c := range h.Network().ConnsToPeer(of) {
			<-ids.IdentifyWait(c)
		}
	}
	require.Eventually(t, func() bool {
		if _, err := mn.ConnectPeers(a.ID(), b.ID()); err != nil {
			return false
		}
		identified(a, b.ID())
		identified(b, a.ID())
		if gossips(a, b.ID()) && gossips(b, a.ID()) {
			return true
		}
		// A connection is identified once; the next one is identified afresh,
		// and one left open beside it does no harm.
		mn.DisconnectPeers(a.ID(), b.ID())
		return false
	}, 30*time.Second, 10*time.Millisecond, "%s and %s never identified each other as gossiping", a.ID(), b.ID())
}

func pair(t *testing.T) (node, node) {
	t.Helper()
	nodes := swarmOf(t, 2)
	return nodes[0], nodes[1]
}

func TestEveryNodeOfALargeSwarmHasAStreamToEveryOther(t *testing.T) {
	nodes := swarmOf(t, 16)

	for i, from := range nodes {
		peers := from.gossip.ListPeers("")
		for j, to := range nodes {
			if i != j {
				require.Contains(t, peers, to.host.ID(), "node %d has no stream to node %d", i, j)
			}
		}
	}
}

func ledger() database.Spec {
	return database.Spec{
		Type: database.SQL, Engine: distributed.Name, Consensus: database.CRDT,
		Project: "acme", Environment: "prod", Name: "ledger",
	}
}

func TestTwoNodesHoldOneDatabase(t *testing.T) {
	left, right := pair(t)
	ctx := context.Background()

	l, err := left.engine.Open(ctx, ledger())
	require.NoError(t, err)
	defer l.Close()
	r, err := right.engine.Open(ctx, ledger())
	require.NoError(t, err)
	defer r.Close()

	lc := l.(database.SQLConn)
	_, err = lc.Exec(ctx, `CREATE TABLE entries (id INTEGER PRIMARY KEY, payee TEXT UNIQUE, amount INTEGER)`)
	require.NoError(t, err)
	_, err = lc.Exec(ctx, `INSERT INTO entries(payee, amount) VALUES(?, ?)`, "alice", 42)
	require.NoError(t, err)

	rc := r.(database.SQLConn)
	require.Eventually(t, func() bool {
		rows, err := rc.Query(ctx, `SELECT payee, amount FROM entries`)
		if err != nil {
			return false
		}
		defer rows.Close()
		var payee string
		var amount int64
		return rows.Next() && rows.Scan(&payee, &amount) == nil && payee == "alice" && amount == 42
	}, 30*time.Second, 100*time.Millisecond, "the row never crossed the swarm")
}

func TestADatabaseIsOpenOncePerNode(t *testing.T) {
	left, _ := pair(t)
	ctx := context.Background()

	c, err := left.engine.Open(ctx, ledger())
	require.NoError(t, err)
	defer c.Close()

	_, err = left.engine.Open(ctx, ledger())
	require.ErrorContains(t, err, "already open in this process")
}
