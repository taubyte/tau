package swarm_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/samyfodil/musql/replication"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/database/engines/sql/distributed/swarm"
	sqlsyncv2 "github.com/taubyte/tau/pkg/proto/gen/tau/sqlsync/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/sqlsync/v2/sqlsyncv2connect"
)

// ANTI-ENTROPY IS WHAT A COPY THAT MISSED THE PUSH HAS. A write is pushed over
// gossip as it is made, so two copies open together never need the service;
// these open them apart, with musql's round run often or not at all, so the
// generated service is the only way a row can cross.

const ledgerTopic = "tau/sql/acme/prod/ledger"

// copyOf is one copy of the ledger on n's mesh, running an anti-entropy round
// every interval.
func copyOf(t *testing.T, n node, every time.Duration) *sql.DB {
	t.Helper()
	db, err := replication.Open(t.Context(), filepath.Join(t.TempDir(), "ledger.db"),
		replication.CRDT(), n.mesh.Sync(ledgerTopic), replication.WithSyncInterval(every))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func write(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `CREATE TABLE entries (id INTEGER PRIMARY KEY, payee TEXT UNIQUE, amount INTEGER)`)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `INSERT INTO entries(payee, amount) VALUES(?, ?)`, "alice", 42)
	require.NoError(t, err)
}

func holdsTheRow(t *testing.T, db *sql.DB, why string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var payee string
		var amount int64
		err := db.QueryRowContext(t.Context(), `SELECT payee, amount FROM entries`).Scan(&payee, &amount)
		return err == nil && payee == "alice" && amount == 42
	}, 30*time.Second, 20*time.Millisecond, why)
}

// service is the anti-entropy service another node answers, dialled from n.
func service(t *testing.T, n, of node) sqlsyncv2connect.SQLSyncServiceClient {
	t.Helper()
	c, err := rpc.NewClient(n.host, of.host.ID(), swarm.Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return sqlsyncv2connect.NewSQLSyncServiceClient(c.HTTP(), c.URL())
}

func TestALateCopyCatchesUpThroughAntiEntropy(t *testing.T) {
	left, right := pair(t)

	write(t, copyOf(t, left, time.Hour))
	holdsTheRow(t, copyOf(t, right, 20*time.Millisecond), "the late copy never pulled the row")
}

func TestALateCopyOfAPrunedLogTakesASnapshot(t *testing.T) {
	left, right := pair(t)

	write(t, copyOf(t, left, 20*time.Millisecond))

	asked := service(t, right, left)
	require.Eventually(t, func() bool {
		vv, err := asked.ExchangeVV(t.Context(), connect.NewRequest(&sqlsyncv2.ExchangeVVRequest{Database: ledgerTopic}))
		if err != nil || len(vv.Msg.GetVv()) == 0 {
			return false
		}
		ops, err := asked.GetOps(t.Context(), connect.NewRequest(&sqlsyncv2.GetOpsRequest{
			Database: ledgerTopic, Site: vv.Msg.GetVv()[0].GetSite(), Limit: 10,
		}))
		return err == nil && ops.Msg.GetPruned()
	}, 30*time.Second, 20*time.Millisecond, "the copy alone never pruned its log")

	holdsTheRow(t, copyOf(t, right, 20*time.Millisecond), "the late copy never took the snapshot")
}

func TestEveryCallForADatabaseANodeDoesNotHoldIsNotFound(t *testing.T) {
	left, right := pair(t)
	asked := service(t, right, left)
	ctx := t.Context()

	for call, ask := range map[string]func() error{
		"ExchangeVV": func() error {
			_, err := asked.ExchangeVV(ctx, connect.NewRequest(&sqlsyncv2.ExchangeVVRequest{Database: ledgerTopic}))
			return err
		},
		"GetOps": func() error {
			_, err := asked.GetOps(ctx, connect.NewRequest(&sqlsyncv2.GetOpsRequest{Database: ledgerTopic}))
			return err
		},
		"ExchangeAcks": func() error {
			_, err := asked.ExchangeAcks(ctx, connect.NewRequest(&sqlsyncv2.ExchangeAcksRequest{Database: ledgerTopic}))
			return err
		},
		"GetSnapshot": func() error {
			_, err := asked.GetSnapshot(ctx, connect.NewRequest(&sqlsyncv2.GetSnapshotRequest{Database: ledgerTopic}))
			return err
		},
	} {
		err := ask()
		require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), call)
		require.ErrorContains(t, err, swarm.ErrUnknownDatabase.Error(), call)
	}
}

func TestASnapshotPartTheStoreDoesNotHaveIsRefused(t *testing.T) {
	left, right := pair(t)
	copyOf(t, left, time.Hour)

	_, err := service(t, right, left).GetSnapshot(t.Context(), connect.NewRequest(&sqlsyncv2.GetSnapshotRequest{Database: ledgerTopic, Part: 99}))
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestAMalformedPushIsDroppedAndTheNextStillLands(t *testing.T) {
	nodes := swarmOf(t, 3)
	left, right, stranger := nodes[0], nodes[1], nodes[2]
	l, r := copyOf(t, left, time.Hour), copyOf(t, right, time.Hour)

	topic, err := stranger.gossip.Join(ledgerTopic)
	require.NoError(t, err)
	t.Cleanup(func() { topic.Close() })
	require.Eventually(t, func() bool {
		peers := topic.ListPeers()
		return slices.Contains(peers, left.host.ID()) && slices.Contains(peers, right.host.ID()) &&
			slices.Contains(left.gossip.ListPeers(ledgerTopic), right.host.ID())
	}, 30*time.Second, 10*time.Millisecond, "the stranger, or the writer, never learned who holds the ledger")

	empty, err := proto.Marshal(&sqlsyncv2.Push{})
	require.NoError(t, err)
	require.NoError(t, topic.Publish(t.Context(), []byte("not a push")))
	require.NoError(t, topic.Publish(t.Context(), empty))

	write(t, l)
	holdsTheRow(t, r, "the push after the malformed ones never landed")
}

func TestAMeshIsRefusedWithoutAHostARouterOrAFreeProtocol(t *testing.T) {
	left, _ := pair(t)

	_, err := swarm.New(t.Context(), nil, left.gossip)
	require.Error(t, err)
	_, err = swarm.New(t.Context(), left.host, nil)
	require.Error(t, err)

	_, err = swarm.New(t.Context(), left.host, left.gossip)
	require.ErrorIs(t, err, rpc.ErrProtocolTaken)
}

var errRefused = errors.New("this router joins nothing")

// refusing is a router that joins no topic.
type refusing struct{}

func (refusing) Join(string, ...pubsub.TopicOpt) (*pubsub.Topic, error) { return nil, errRefused }

// closing joins a topic and closes it before handing it back, so nothing can
// subscribe to it.
type closing struct{ ps *pubsub.PubSub }

func (c closing) Join(topic string, opts ...pubsub.TopicOpt) (*pubsub.Topic, error) {
	t, err := c.ps.Join(topic, opts...)
	if err != nil {
		return nil, err
	}
	return t, t.Close()
}

func TestACopyThatCannotGossipIsNotOpened(t *testing.T) {
	type testCase struct {
		name   string
		router func(*pubsub.PubSub) swarm.PubSub
		err    error
	}
	for _, tc := range []testCase{
		{"the topic cannot be joined", func(*pubsub.PubSub) swarm.PubSub { return refusing{} }, errRefused},
		{"the topic cannot be subscribed to", func(ps *pubsub.PubSub) swarm.PubSub { return closing{ps} }, pubsub.ErrTopicClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mn := mocknet.New()
			t.Cleanup(func() { mn.Close() })
			h, err := mn.GenPeer()
			require.NoError(t, err)
			asker, err := mn.GenPeer()
			require.NoError(t, err)
			require.NoError(t, mn.LinkAll())
			ps, err := pubsub.NewGossipSub(t.Context(), h)
			require.NoError(t, err)

			m, err := swarm.New(t.Context(), h, tc.router(ps))
			require.NoError(t, err)
			t.Cleanup(func() { m.Close() })

			_, err = replication.Open(t.Context(), filepath.Join(t.TempDir(), "ledger.db"), replication.CRDT(), m.Sync(ledgerTopic))
			require.ErrorIs(t, err, tc.err)

			c, err := rpc.NewClient(asker, h.ID(), swarm.Protocol)
			require.NoError(t, err)
			t.Cleanup(c.Close)
			_, err = sqlsyncv2connect.NewSQLSyncServiceClient(c.HTTP(), c.URL()).
				ExchangeVV(t.Context(), connect.NewRequest(&sqlsyncv2.ExchangeVVRequest{Database: ledgerTopic}))
			require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
		})
	}
}
