package swarm_test

import (
	"context"
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
	engine *distributed.Engine
	mesh   *swarm.Mesh
}

func pair(t *testing.T) (node, node) {
	t.Helper()
	ctx := t.Context()
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	var out []node
	for range 2 {
		h, err := mn.GenPeer()
		require.NoError(t, err)
		out = append(out, node{host: h})
	}
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	for i := range out {
		ps, err := pubsub.NewGossipSub(ctx, out[i].host)
		require.NoError(t, err)
		m, err := swarm.New(ctx, out[i].host, ps)
		require.NoError(t, err)
		t.Cleanup(func() { m.Close() })
		e, err := distributed.New(distributed.InDir(t.TempDir()), distributed.WithSync(m.Sync))
		require.NoError(t, err)
		out[i].mesh, out[i].engine = m, e
	}
	return out[0], out[1]
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
