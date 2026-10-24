package swarm_test

import (
	"context"
	"slices"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
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

// swarmOf is n nodes on one mocknet, every one with a gossip stream to every
// other before a mesh is built on it.
//
// EVERY HOST GOSSIPS BEFORE IT IS CONNECTED. A gossipsub opens its stream to a
// peer when it starts or when identify says the peer gossips, and never retries
// one that failed. A host connected before its peer gossiped hears of it only
// from an identify push: sent once, and limited per subnet, which every mocknet
// peer shares. Miss it and that host never sends the peer a write, nor tells it
// that it holds the database.
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

func pair(t *testing.T) (node, node) {
	t.Helper()
	nodes := swarmOf(t, 2)
	return nodes[0], nodes[1]
}

// EVERY NODE OF A LARGE SWARM HAS A STREAM TO EVERY OTHER when it is handed
// over. A host takes ten identify pushes in a burst from one subnet, then one
// every five seconds, so sixteen mocknet hosts are past it: a swarm that
// connected before it gossiped left some nodes never sending to others.
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

// A WRITE ON ONE NODE IS READ ON THE OTHER, over the real stack: the generated
// service on a libp2p stream for anti-entropy and gossipsub for the push. The
// schema is a guest's, created after the database is open.
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

// A database is opened once per node: musql refuses a second open of one file,
// so its topic is never joined twice on one router.
func TestADatabaseIsOpenOncePerNode(t *testing.T) {
	left, _ := pair(t)
	ctx := context.Background()

	c, err := left.engine.Open(ctx, ledger())
	require.NoError(t, err)
	defer c.Close()

	_, err = left.engine.Open(ctx, ledger())
	require.ErrorContains(t, err, "already open in this process")
}
