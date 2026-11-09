package hosted

import (
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/pkg/database"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/worker"
	nodeDatabase "github.com/taubyte/tau/services/worker/database"
)

func TestAWorkerLeftToItsDefaultsOpensPostgresAndMysql(t *testing.T) {
	n := sqlWorker(t, Config{NoRoot: true})
	require.ElementsMatch(t, []string{"postgres", "mysql"}, n.engines.EnginesFor(database.SQL))
}

func TestAWorkerOpensOnlyTheEnginesItsSectionNames(t *testing.T) {
	n := sqlWorker(t, Config{NoRoot: true, Worker: worker.Config{
		Databases: []worker.Database{{Engine: "mysql", Driver: "mysql"}},
	}})
	require.Equal(t, []string{"mysql"}, n.engines.EnginesFor(database.SQL))
}

func TestTheDatabasesAreWrittenInTheWorkersSection(t *testing.T) {
	var c hostConfig
	require.NoError(t, yaml.Unmarshal([]byte("databases:\n  - engine: postgres\n    driver: pgx\n"), &c))
	require.Equal(t, []worker.Database{{Engine: "postgres", Driver: "pgx"}}, c.Databases)
}

func TestANamedEngineOverADriverThisBinaryLacksIsRefused(t *testing.T) {
	_, _, err := startNode(t, Config{Services: []string{Stats, Worker}, Worker: worker.Config{
		Databases: []worker.Database{{Engine: "postgres", Driver: "nosuch"}},
	}})
	require.ErrorIs(t, err, ErrNoDriver)
	require.ErrorContains(t, err, "nosuch")
}

func TestAnEngineTheWorkerCannotAdaptIsRefused(t *testing.T) {
	_, _, err := startNode(t, Config{Services: []string{Stats, Worker}, Worker: worker.Config{
		Databases: []worker.Database{{Engine: "mongo", Driver: "mysql"}},
	}})
	require.ErrorContains(t, err, `"mongo" is not an external database engine`)
}

func TestEveryExternalDatabaseEngineIsAdapted(t *testing.T) {
	n := sqlWorker(t, Config{})

	registered := map[string]map[string]bool{}
	for _, model := range []database.Type{database.KV, database.Document, database.SQL, database.Vector} {
		registered[string(model)] = map[string]bool{}
		for _, name := range n.engines.EnginesFor(model) {
			registered[string(model)][name] = true
		}
	}

	// tau's own engines are the node's, but only for the models it backs, and sql,
	// which is a musql file per database the worker registers itself.
	tauEngine := schema.TauEngine()
	tauBacks := map[string]bool{string(database.SQL): true}
	for _, model := range nodeDatabase.TauBacked() {
		tauBacks[string(model)] = true
	}

	// absent is every engine nothing here opens, keyed by model because an engine is
	// not absent everywhere.
	absent := map[string]string{
		"document/mongo": "no document adapter yet; the vocabulary declares the engine and nothing opens one",
	}

	var missing []string
	for model, engines := range schema.DatabaseEngines() {
		for _, engine := range engines {
			if engine == tauEngine && tauBacks[model] || registered[model][engine] {
				continue
			}
			if _, known := absent[model+"/"+engine]; known {
				continue
			}
			missing = append(missing, model+"/"+engine)
		}
	}
	require.Empty(t, missing,
		"the vocabulary names a database engine a worker cannot open: %v. A project declaring it "+
			"compiles, deploys, and answers every query with \"no database engine is registered\".", missing)
}
