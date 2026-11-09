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
	require.NotNil(t, n.engines)
	require.Contains(t, n.engines.EnginesFor(database.SQL), distributed.Name)
}

func TestAWorkerWithNoRootServesNoDistributedSQL(t *testing.T) {
	n := sqlWorker(t, Config{NoRoot: true})
	require.Nil(t, n.engines, "no root, so no registry was made to hold one")
}

func TestAComposersDistributedEngineIsKept(t *testing.T) {
	reg := database.NewRegistry()
	mine, err := distributed.New(distributed.UnderRoot(t.TempDir()))
	require.NoError(t, err)
	require.NoError(t, reg.Register(t.Context(), mine))

	n := sqlWorker(t, Config{}, WithDatabaseEngines(reg))
	require.Same(t, reg, n.engines)
	require.Equal(t, []string{distributed.Name}, n.engines.EnginesFor(database.SQL))
}
