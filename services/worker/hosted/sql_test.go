package hosted

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/database/engines/sql/distributed"
)

func sqlWorker(t *testing.T, cfg Config, extra ...Option) *node {
	t.Helper()
	cfg.Services = []string{Stats, Worker}
	n, _, _ := makeNode(t, cfg, extra...)
	return n
}

func TestAWorkerWithARootServesDistributedSQL(t *testing.T) {
	n := sqlWorker(t, Config{})
	require.Contains(t, n.engines.EnginesFor(database.SQL), distributed.Name)
}

func TestAWorkerWithNoRootServesNoDistributedSQL(t *testing.T) {
	n := sqlWorker(t, Config{NoRoot: true})
	require.NotContains(t, n.engines.EnginesFor(database.SQL), distributed.Name)
}
