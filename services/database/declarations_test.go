package database

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// declaring builds a declarations over one compiled project, so a test can ask
// what a database IS without standing up a service to ask through.
func declaring(t *testing.T, root map[string]any) *declarations {
	t.Helper()

	doc := map[string]any{
		"object": root,
		"indexes": map[string]any{
			"projects/" + projectID + "/database/sessions/links": []any{
				"branches/main/projects/" + projectID + "/database/" + kvID},
			"projects/" + projectID + "/database/knowledge/links": []any{
				"branches/main/projects/" + projectID + "/database/" + vectorID},
		},
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)

	projects, err := register.NewCache(&published{revision: 1, object: raw}, specs.Load)
	require.NoError(t, err)
	return &declarations{projects: projects}
}

// kvAt is one kv database, with whatever region block the case is about.
func kvAt(region map[string]any) map[string]any {
	out := map[string]any{
		"name": "sessions", "type": "kv",
		"kv": map[string]any{"engine": "distributed", "consensus": "crdt"},
	}
	if region != nil {
		out["regions"] = region
	}
	return out
}

func sessions(t *testing.T, d *declarations) declared {
	t.Helper()
	got, err := d.of(context.Background(), address{project: projectID, environment: "prod", id: kvID})
	require.NoError(t, err)
	return got
}

// TestADatabaseInheritsTheScopesRegions, which is how a project says where
// things go ONCE rather than on every resource. It is the SAME WORD a resource
// writes — one concept narrowing down, rather than an allowance and a placement
// that differ by a letter.
//
// NOTHING LOWERED IT INTO THE DATABASE: the compiler writes no placement a
// resource did not author, so this is the walk up. A copy would be a fact
// decompile could not give back.
func TestADatabaseInheritsTheProjectsPlacement(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"regions":  []string{"eu"},
		"database": map[string]any{kvID: kvAt(nil)},
	})

	require.Equal(t, []string{"eu"}, sessions(t, d).regions)
}

// TestADatabasesOwnPlacementWins. The narrower statement decides; the project's
// is what everything that said nothing falls back to.
func TestADatabasesOwnPlacementWins(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"regions": []string{"eu", "us", "ap"},
		"database": map[string]any{kvID: kvAt(
			map[string]any{"policy": "failover", "main": "us", "standby": "ap"})},
	})

	require.Equal(t, []string{"ap", "us"}, sessions(t, d).regions,
		"the project's default overrode what the resource itself said")
}

// TestNeitherIsNowhere. The audit calls this Broken at compile; if one reaches a
// runtime anyway it is placed across the allowance rather than guessed at.
func TestNeitherIsNowhere(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"database": map[string]any{kvID: kvAt(nil)},
	})

	require.Equal(t, []string{anyRegion}, sessions(t, d).regions)
}

// TestAShadowKeepsStateWhereItWasWrittenInside. A shadow is a deployment over
// another environment's state, so placing that state was never its to do — the
// vocabulary gives it no regions field at all.
func TestAShadowTakesTheAllowanceItWasWrittenInside(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"regions": []string{"eu", "us"},
		"environments": map[string]any{
			"production": map[string]any{
				"branches": []string{"main"},
				"regions":  []string{"eu"},
				"shadows":  map[string]any{"ab": map[string]any{"branches": []string{"main"}}},
			},
		},
		"database": map[string]any{kvID: kvAt(nil)},
	})

	for _, env := range []string{"production", "ab"} {
		got, err := d.of(context.Background(), address{project: projectID, environment: env, id: kvID})
		require.NoError(t, err, env)
		require.Equal(t, []string{"eu"}, got.regions, "%s did not keep state where production does", env)
	}
}

// TestAnEnvironmentThatNamesNoRegionsTakesTheProjects, because `regions` is one
// word at every level: a scope's is where everything under it may be AND where
// anything that does not say otherwise is placed.
func TestAnEnvironmentInheritsTheProjectsRegions(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"regions": []string{"eu", "us"},
		"environments": map[string]any{
			"prod": map[string]any{"branches": []string{"main"}},
		},
		"database": map[string]any{kvID: kvAt(nil)},
	})

	require.Equal(t, []string{"eu", "us"}, sessions(t, d).regions)
}

// TestWHATONLYONEMODELUNDERSTANDSTRAVELSASANOPTION.
//
// A vector's width and its distance metric mean nothing to the other three
// models, so they do not get a field each on the spec — they are carried under
// the vocabulary's own attribute names, which is what lets a reader find the
// attribute by searching the schema for the string.
func TestPerModelDeclarationsTravelAsOptions(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"regions": []string{"eu"},
		"database": map[string]any{
			vectorID: map[string]any{
				"name": "knowledge", "type": "vector",
				"vector": map[string]any{
					"engine": "distributed", "consensus": "crdt",
					"dimensions": float64(1536), "metric": "cosine",
				},
			},
		},
	})

	// READING A DECLARATION IS NOT AGREEING TO SERVE IT. `of` reads what the
	// author wrote; whether this cloud serves that model is asked separately,
	// so a model nothing here implements still resolves.
	got, err := d.of(context.Background(),
		address{project: projectID, environment: "prod", id: vectorID})
	require.NoError(t, err)

	require.Equal(t, "1536", got.spec.Options["dimensions"])
	require.Equal(t, "cosine", got.spec.Options["metric"])

	// AND IT IS REFUSED BY NAME when somebody asks this cloud to serve it.
	require.ErrorIs(t, got.serves(servedConsensus), ErrModelUnserved)
}

// TestACONSENSUSNOBODYWROTEISTHEDEFAULT, which is what a connector gets: it
// resolves nothing, because somebody else's server does.
//
// IT IS RESOLVED ONTO THE SPEC AND NOT ASKED FOR LATER, so the provider that
// picks a store is handed a spec that always says which one.
func TestAnUnwrittenConsensusIsTheDefault(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"database": map[string]any{kvID: map[string]any{
			"name": "sessions", "type": "kv",
			"kv": map[string]any{"engine": "distributed"},
		}},
	})

	require.Equal(t, defaultConsensus, sessions(t, d).spec.Consensus)
	require.Equal(t, defaultConsensus, sessions(t, d).wants())
	require.Equal(t, database.Raft, declared{spec: database.Spec{Consensus: database.Raft}}.wants())
}

// TestWHATTHISNODECANKEEPISTHECOMPOSERSANSWER. Both resolutions are served by
// this package; which of them a NODE can keep depends on whether it was handed
// somewhere to open a group, and a node that was not refuses by name rather
// than answering unavailable forever.
func TestAConsensusThisNodeWasNotGivenIsRefusedByName(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"database": map[string]any{kvID: map[string]any{
			"name": "ledger", "type": "kv",
			"kv": map[string]any{"engine": "distributed", "consensus": "raft"},
		}},
	})

	ledger := sessions(t, d)
	require.ErrorIs(t, ledger.serves(servedConsensus), ErrConsensusUnserved)
	require.ErrorContains(t, ledger.serves(servedConsensus), "raft")
	require.NoError(t, ledger.serves([]string{database.CRDT, database.Raft}))
}

// TestEVERYMODELSCONNECTIONISREADOFFITSOWNBLOCK. The engines a model may have
// differ, so each reads its own block, and a model that has no address, no
// credential and no tls reads them empty rather than off another model's.
func TestEveryModelsConnectionIsReadOffItsOwnBlock(t *testing.T) {
	size := int64(42)
	sql := blockOf(specs.Database{Type: specs.DatabaseTypeSql, Sql: specs.DatabaseSql{
		Engine: specs.DatabaseSqlEnginePostgres, Host: "db:5432", Database: "shop",
		Consensus: specs.DatabaseSqlConsensusRaft, Tls: specs.DatabaseSqlTlsDisable, Size: &size,
	}})
	require.Equal(t, typeBlock{specs.DatabaseSqlEnginePostgres, "db:5432", "shop", specs.DatabaseSqlConsensusRaft, &size, true}, sql)

	document := blockOf(specs.Database{Type: specs.DatabaseTypeDocument, Document: specs.DatabaseDocument{
		Engine: specs.DatabaseDocumentEngineMongo, Host: "mongo", Database: "docs", Tls: specs.DatabaseDocumentTlsRequire,
	}})
	require.Equal(t, "mongo", document.host)
	require.False(t, document.plaintext, "require is not disable")

	kv := blockOf(specs.Database{Type: specs.DatabaseTypeKv, Kv: specs.DatabaseKv{Engine: specs.DatabaseKvEngineDistributed, Size: &size}})
	require.Equal(t, typeBlock{engine: specs.DatabaseKvEngineDistributed, size: &size}, kv)

	vector := blockOf(specs.Database{Type: specs.DatabaseTypeVector, Vector: specs.DatabaseVector{Consensus: specs.DatabaseVectorConsensusCrdt}})
	require.Equal(t, specs.DatabaseVectorConsensusCrdt, vector.consensus)

	require.Equal(t, typeBlock{}, blockOf(specs.Database{}))
}
