package boot

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	nodeDatabase "github.com/taubyte/tau/services/worker/database"
)

// Every external engine the vocabulary names is one a machine can open.

func TestTheExternalSqlEnginesRegister(t *testing.T) {
	registry, err := DatabaseEngines(t.Context())
	require.NoError(t, err)

	names := map[string]bool{}
	for _, e := range registry.EnginesFor(database.SQL) {
		names[e] = true
	}
	require.True(t, names["postgres"], "registered: %v", names)
	require.True(t, names["mysql"], "registered: %v", names)
}

func TestEveryExternalDatabaseEngineIsAdapted(t *testing.T) {
	registry, err := DatabaseEngines(t.Context())
	require.NoError(t, err)

	// registered is what a machine can open, by data model.
	registered := map[string]map[string]bool{}
	for _, model := range []database.Type{database.KV, database.Document, database.SQL, database.Vector} {
		registered[string(model)] = map[string]bool{}
		for _, name := range registry.EnginesFor(model) {
			registered[string(model)][name] = true
		}
	}

	// tau's own engines are the node's, but only for the models it backs
	// (nodeDatabase.TauBacked: kv, document, vector).
	tauEngine := schema.TauEngine()
	tauBacks := map[string]bool{}
	for _, model := range nodeDatabase.TauBacked() {
		tauBacks[string(model)] = true
	}
	// AND sql, which is not the swarm's store but a musql file per database: the
	// node registers that engine itself on every node with a root, a host and a
	// pubsub (instance/sql.go), and instance/dream's sql project proves it opens.
	tauBacks[string(database.SQL)] = true

	// absent is every engine nothing here opens, keyed by model, with the
	// reason. KEYED BY MODEL because an engine is not absent everywhere: tau's
	// own is real for three data models and a promise for the fourth.
	absent := map[string]string{
		"document/mongo": "no document adapter yet; the vocabulary declares the engine and nothing opens one",
	}

	var missing []string
	for model, engines := range schema.DatabaseEngines() {
		for _, engine := range engines {
			if engine == tauEngine && tauBacks[model] {
				continue
			}
			if registered[model][engine] {
				continue
			}
			if _, known := absent[model+"/"+engine]; known {
				continue
			}
			missing = append(missing, model+"/"+engine)
		}
	}

	require.Empty(t, missing,
		"the vocabulary names a database engine a machine cannot open: %v. A project declaring it "+
			"compiles, deploys, and answers every query with \"no database engine is registered\".", missing)
}
