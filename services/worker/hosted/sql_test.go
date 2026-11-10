package hosted

import (
	"testing"

	"github.com/stretchr/testify/require"

	taupeer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/database/engines/sql/distributed"
)

func sqlWorker(t *testing.T, mn *taupeer.Mocknet, cfg Config, extra ...Option) *node {
	t.Helper()
	cfg.Services = []string{Stats, Worker}
	n, _, _ := makeNode(t, mn, cfg, extra...)
	return n
}

func TestAWorkerWithARootServesDistributedSQL(t *testing.T) {
	mn := peertest.Net(t)
	n := sqlWorker(t, mn, Config{})
	require.Contains(t, n.engines.EnginesFor(database.SQL), distributed.Name)
}

func TestAWorkerWithNoRootServesNoDistributedSQL(t *testing.T) {
	mn := peertest.Net(t)
	n := sqlWorker(t, mn, Config{NoRoot: true})
	require.NotContains(t, n.engines.EnginesFor(database.SQL), distributed.Name)
}
