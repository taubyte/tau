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

func sessions(t *testing.T, d *declarations) declared {
	t.Helper()
	got, err := d.of(context.Background(), address{project: projectID, environment: "prod", id: kvID})
	require.NoError(t, err)
	return got
}

func TestPerModelDeclarationsTravelAsOptions(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"regions": []string{"region-1"},
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

	// Reading a declaration is not agreeing to serve it.
	got, err := d.of(context.Background(),
		address{project: projectID, environment: "prod", id: vectorID})
	require.NoError(t, err)

	require.Equal(t, "1536", got.spec.Options["dimensions"])
	require.Equal(t, "cosine", got.spec.Options["metric"])

	// AND IT IS REFUSED BY NAME when somebody asks this cloud to serve it.
	require.ErrorIs(t, got.serves(servedConsensus), ErrModelUnserved)
}

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
