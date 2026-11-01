package distributed_test

import (
	"context"
	"testing"
	"time"

	"github.com/samyfodil/musql/replication"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/database/engines/sql/distributed"
)

// One database on two engines joined by musql's in-memory network. The guest's schema
// (an integer key and a UNIQUE column, created after open) is what replicates.

// meshed is a sync option that joins every engine handed one to one network,
// and the topics it was asked for.
func meshed(t *testing.T, net *replication.MemNetwork, node string, topics *[]string) distributed.Option {
	t.Helper()
	return distributed.WithSync(func(topic string) replication.Option {
		*topics = append(*topics, topic)
		return replication.WithTransport(func(_ context.Context, s *replication.Syncer) (replication.Transport, error) {
			return net.AddNode(replication.PeerID(node), s.OpSource()), nil
		})
	})
}

func TestAWriteOnOneNodeIsReadOnTheOther(t *testing.T) {
	net := replication.NewMemNetwork()
	var topics []string

	left, err := distributed.New(distributed.InDir(t.TempDir()), meshed(t, net, "left", &topics))
	require.NoError(t, err)
	right, err := distributed.New(distributed.InDir(t.TempDir()), meshed(t, net, "right", &topics))
	require.NoError(t, err)

	ctx := context.Background()
	spec := ledger(t)
	spec.Consensus = database.CRDT

	l, err := left.Open(ctx, spec)
	require.NoError(t, err)
	defer l.Close()
	r, err := right.Open(ctx, spec)
	require.NoError(t, err)
	defer r.Close()

	require.Equal(t, []string{"tau/sql/acme/prod/sql/ledger", "tau/sql/acme/prod/sql/ledger"}, topics,
		"both copies of one database sync on one topic, named by the database")

	lc := l.(database.SQLConn)
	_, err = lc.Exec(ctx, `CREATE TABLE entries (id INTEGER PRIMARY KEY, payee TEXT UNIQUE, amount INTEGER)`)
	require.NoError(t, err)
	_, err = lc.Exec(ctx, `INSERT INTO entries(payee, amount) VALUES(?, ?)`, "alice", 42)
	require.NoError(t, err)

	rc := r.(database.SQLConn)
	require.Eventually(t, func() bool {
		rows, err := rc.Query(ctx, `SELECT payee, amount FROM entries`)
		if err != nil {
			return false // the table itself has not arrived yet
		}
		defer rows.Close()
		var payee string
		var amount int64
		return rows.Next() && rows.Scan(&payee, &amount) == nil && payee == "alice" && amount == 42
	}, 20*time.Second, 100*time.Millisecond, "the row written on one node never reached the other")
}

func TestADatabaseIsServedOnlyWithTheConsensusItDeclared(t *testing.T) {
	e, err := distributed.New(distributed.InDir(t.TempDir()))
	require.NoError(t, err)

	spec := ledger(t)
	spec.Consensus = database.Raft
	_, err = e.Open(context.Background(), spec)
	require.ErrorIs(t, err, distributed.ErrRaft)

	spec.Consensus = "gossip"
	_, err = e.Open(context.Background(), spec)
	require.ErrorContains(t, err, `consensus "gossip"`)
}

func TestADatabaseStopsAtItsDeclaredSize(t *testing.T) {
	e, err := distributed.New(distributed.InDir(t.TempDir()))
	require.NoError(t, err)

	spec := ledger(t)
	spec.Consensus = database.CRDT
	spec.Size = 256 << 10
	c, err := e.Open(context.Background(), spec)
	require.NoError(t, err)
	defer c.Close()

	sc := c.(database.SQLConn)
	ctx := context.Background()
	_, err = sc.Exec(ctx, `CREATE TABLE blobs (id INTEGER PRIMARY KEY, b BLOB)`)
	require.NoError(t, err)

	big := make([]byte, 64<<10)
	var full error
	for range 64 {
		if _, err := sc.Exec(ctx, `INSERT INTO blobs(b) VALUES(?)`, big); err != nil {
			full = err
			break
		}
	}
	require.ErrorContains(t, full, "database or disk is full")
}
