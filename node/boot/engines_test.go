package boot

import (
	"testing"

	"github.com/stretchr/testify/require"

	nodeDatabase "github.com/taubyte/tau/node/database"
	"github.com/taubyte/tau/pkg/database"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// engines_test is the one direction that matters: EVERY EXTERNAL ENGINE THE
// VOCABULARY NAMES IS ONE A MACHINE CAN OPEN.
//
// A project writing `sql: {postgres: {host: db.example.com}}` compiles and
// deploys whatever this file says. Before it, that project answered its first
// query with "no database engine is registered in this build" — discovered by a
// user rather than by us, which is the shape this repository keeps deleting.

// TestTheDriversAreLinkedAndTheEnginesRegister. A registration opens a probe
// connection and checks the surface, so this also proves the adapters answer
// the operations `sql` declares.
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

// TestEVERYEXTERNALENGINETheVocabularyNamesCanBeOpened, and the ones that
// cannot are listed here as knowingly absent.
//
// A fifth engine added to the schema with no adapter is a project that
// compiles, deploys and refuses every query — and this is where that is
// noticed rather than in somebody's logs.
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

	// TAU'S OWN ARE THE NODE'S TO REGISTER and are not in this registry — it
	// opens them on first use against the swarm's store, so they are absent
	// here by design rather than missing.
	//
	// FOR THE MODELS IT ACTUALLY BACKS, and that qualifier is the whole fix.
	// This used to skip tau's engine for EVERY model, which assumed the node
	// opens a distributed one for all four. It does not: nodeDatabase.TauBacked() is
	// kv, document and vector, and `sql: {engine: distributed}` compiled,
	// deployed and answered every query with "no database engine is registered
	// in this build" — with this test green over it, because this was the line
	// that looked away.
	tauEngine := schema.TauEngine()
	tauBacks := map[string]bool{}
	for _, model := range nodeDatabase.TauBacked() {
		tauBacks[string(model)] = true
	}
	// AND sql, which is not the swarm's store but a musql file per database: the
	// node registers that engine itself on every node with a root, a host and a
	// pubsub (node/sql.go), and node/dream's sql project proves it opens.
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
