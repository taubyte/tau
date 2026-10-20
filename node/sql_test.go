package node

import (
	"testing"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/database/engines/sql/distributed"
	"github.com/taubyte/tau/pkg/raft"
)

func sqlWorker(t *testing.T, extra ...Option) *Node {
	t.Helper()
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)
	n, err := New(Config{Services: []string{Discovery, Worker}}, append([]Option{
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}),
		WithRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }),
		WithAssets(&countingAssets{}),
	}, extra...)...)
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	return n
}

// A WORKER WITH A ROOT OPENS tau's OWN sql ENGINE, in a registry of its own
// when the composer gave none.
func TestAWorkerWithARootServesDistributedSQL(t *testing.T) {
	n := sqlWorker(t, WithRoot(t.TempDir()))
	require.NotNil(t, n.opts.engines)
	require.Contains(t, n.opts.engines.EnginesFor(database.SQL), distributed.Name)
}

// WITHOUT A ROOT IT REGISTERS NONE: rows kept in a temporary directory would
// die with the process.
func TestAWorkerWithNoRootServesNoDistributedSQL(t *testing.T) {
	n := sqlWorker(t)
	require.Nil(t, n.opts.engines, "no root, so no registry was made to hold one")
}

// A COMPOSER'S OWN distributed ENGINE IS KEPT, not replaced by this node's.
func TestAComposersDistributedEngineIsKept(t *testing.T) {
	reg := database.NewRegistry()
	mine, err := distributed.New(distributed.UnderRoot(t.TempDir()))
	require.NoError(t, err)
	require.NoError(t, reg.Register(t.Context(), mine))

	n := sqlWorker(t, WithRoot(t.TempDir()), WithDatabaseEngines(reg))
	require.Same(t, reg, n.opts.engines)
	require.Equal(t, []string{distributed.Name}, n.opts.engines.EnginesFor(database.SQL))
}

func TestANilDatabaseRegistryIsRefused(t *testing.T) {
	require.ErrorContains(t, WithDatabaseEngines(nil)(&options{}), "nil database registry")
}
