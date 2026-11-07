package instance

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/raft"
	databaseService "github.com/taubyte/tau/services/database"
)

// What the root decides about `consensus: raft`: which held thing keeps a database's
// entries, what a node holding neither answers, and that a backing's groups close with
// it. Ledger behaviour is instance/ledger's.

func TestTheGroupAndTheStoreAgreeOnTheNameADatabaseHas(t *testing.T) {
	spec := ledgerSpec()

	group, err := databaseService.LedgerNamespace(spec)
	require.NoError(t, err)
	require.Equal(t, databaseService.LedgerDomain+"/shop/prod/kv/QmLedger", group)

	store, err := databaseService.Name(spec)
	require.NoError(t, err)
	require.Equal(t, databaseService.Prefix+"/shop/prod/kv/QmLedger", store)
}

func TestTheRootPicksBetweenACrdtStoreAndAGroup(t *testing.T) {
	cluster := &closing{MockCluster: raft.NewMockCluster()}

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		withRaft(func(string) (raft.Cluster, error) { return cluster, nil }))
	d, err := newDatabases(n)
	require.NoError(t, err)

	crdt, err := d.holdings.Entries(t.Context(), database.Spec{
		Type: database.KV, Engine: database.Tau, Consensus: database.CRDT,
		Project: "shop", Environment: "prod", Name: "QmSessions",
	})
	require.NoError(t, err)
	require.IsType(t, cacheStore{}, crdt)

	require.Eventually(t, func() bool {
		ledger, err := d.holdings.Entries(t.Context(), ledgerSpec())
		if err != nil {
			return false
		}
		return ledger.Put(t.Context(), "entry-1", []byte("one")) == nil
	}, 10*time.Second, 10*time.Millisecond, "the ledger never reached its group")

	// AND A DATABASE THAT DECLARED NOTHING IS A CRDT ONE, which is what every
	// caller that predates the field keeps getting.
	silent, err := d.holdings.Entries(t.Context(), database.Spec{
		Type: database.KV, Engine: database.Tau,
		Project: "shop", Environment: "prod", Name: "QmSessions",
	})
	require.NoError(t, err)
	require.IsType(t, cacheStore{}, silent)

	// AND THE GROUPS THIS BACKING OPENED GO WITH IT. A raft cluster left running
	// keeps a protocol handler, a heartbeat and a log open on a peer nobody is
	// using any more.
	require.NoError(t, d.Close())
	require.True(t, cluster.shut(), "the backing closed without closing the group it opened")
}

func ledgerSpec() database.Spec {
	return database.Spec{
		Type: database.KV, Engine: database.Tau, Consensus: database.Raft,
		Project: "shop", Environment: "prod", Name: "QmLedger",
	}
}

// closing is a cluster that records its shutdown, which is the one thing the
// root owes instance/ledger.
type closing struct {
	*raft.MockCluster

	mu     sync.Mutex
	closed bool
}

func (c *closing) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.MockCluster.Close()
}

func (c *closing) shut() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
