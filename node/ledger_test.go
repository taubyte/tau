package node

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	nodeDatabase "github.com/taubyte/tau/node/database"
	"github.com/taubyte/tau/node/ledger"
	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/raft"
)

// ledger_test is what the ROOT decides about `consensus: raft`: which of the two
// things this node holds keeps a database's entries, what a node holding neither
// answers, and that the groups a backing opened go when it does.
//
// WHAT A LEDGER ANSWERS IS NOT HERE. The store, the leadership, the barrier and
// the table of groups are node/ledger's, tested there against one function
// instead of against a composed node.

// TestTHEGROUPANDTHESTOREAGREEONTHENAME, by construction rather than by two
// expressions that happen to match: both are Spec.Prefix under a different root,
// and the root is the only place that sees both.
func TestTheGroupAndTheStoreAgreeOnTheNameADatabaseHas(t *testing.T) {
	spec := ledgerSpec()

	group, err := ledger.Namespace(spec)
	require.NoError(t, err)
	require.Equal(t, ledger.Domain+"/shop/prod/kv/QmLedger", group)

	store, err := nodeDatabase.Name(spec)
	require.NoError(t, err)
	require.Equal(t, nodeDatabase.Prefix+"/shop/prod/kv/QmLedger", store)
}

// TestWHICHSTOREADATABASEGETSISITSOWNDECLARATION, and not a second engine: the
// schema declares ONE engine for tau's own, with a consensus beside it. What the
// root decides is only which of the two things it holds answers — the crdt store
// it opens through the factory, or a group node/ledger forms.
func TestTheRootPicksBetweenACrdtStoreAndAGroup(t *testing.T) {
	cluster := &closing{MockCluster: raft.NewMockCluster()}

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery}},
		WithRaft(func(string) (raft.Cluster, error) { return cluster, nil }))
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

// TestANODEWITHNOWHERETOOPENAGROUPKEEPSNOLEDGER, which is what makes the
// service's refusal by name true: the composer either handed this node a way to
// open a group or it did not. ErrNoRaft and not a ledger's own sentence, because
// four things on this node need consensus and what is missing is the same in
// every case — see config.go.
func TestANodeWithNowhereToOpenAGroupKeepsNoLedger(t *testing.T) {
	// BUILT WITHOUT THE HARNESS, which always hands a node a raft opener: what
	// is under test here is the composition that has none.
	d, err := newDatabases(&Node{opts: options{}})
	require.NoError(t, err)

	_, err = d.holdings.Entries(t.Context(), ledgerSpec())
	require.ErrorIs(t, err, ErrNoRaft)

	// AND CLOSING ONE IS NOT A PANIC OVER A TABLE THAT WAS NEVER BUILT.
	require.NoError(t, d.Close())
}

func ledgerSpec() database.Spec {
	return database.Spec{
		Type: database.KV, Engine: database.Tau, Consensus: database.Raft,
		Project: "shop", Environment: "prod", Name: "QmLedger",
	}
}

// closing is a cluster that records its shutdown, which is the one thing the
// root owes node/ledger.
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
