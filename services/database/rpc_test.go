package database

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/database"
	databasev1 "github.com/taubyte/tau/pkg/proto/gen/tau/database/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/database/v1/databasev1connect"
	"github.com/taubyte/tau/pkg/register"
)

// rpc_test drives the service over a real libp2p stream, because the thing
// under test is a SERVICE: what a caller can reach, what it is refused, and
// what the refusal is called.

const (
	projectID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	kvID      = "QmVLDAzEjEbfBPuoyLPWnPMhKp8ceDXwGdmMKY5W3nZWfG"
	sqlID     = "QmSgvGwSJmtqBCJ8Zw3rNKtCCTqTFcqSUQqLYyPBnW6cVs"
	raftID    = "QmZ4gCbmYaFZ4dWJmvNBQKvNAWMYtHqQTHRDprHmxPh6Zk"
)

// compiled is a project with three databases: one this service serves, one of a
// model it does not, and one asking for a consensus it does not keep.
func compiled() []byte {
	doc := map[string]any{
		"object": map[string]any{
			"id":   projectID,
			"name": "shop",
			"database": map[string]any{
				kvID: map[string]any{
					"name": "sessions", "type": "kv",
					"kv": map[string]any{"engine": "distributed", "consensus": "crdt", "size": float64(64 << 20)},
				},
				sqlID: map[string]any{
					"name": "orders", "type": "sql",
					"sql": map[string]any{"engine": "postgres", "host": "db.example.com:5432", "database": "orders"},
				},
				raftID: map[string]any{
					"name": "ledger", "type": "kv",
					"kv": map[string]any{"engine": "distributed", "consensus": "raft", "size": float64(1 << 20)},
				},
			},
		},

		// THE INDEX IS WHAT MAKES AN ID ADDRESSABLE. tcc emits
		// `projects/<pid>/<kind>/<name>/links` per resource, and pkg/specs reads
		// it backwards to answer "what is this id" — which is the only question
		// this service asks of a project.
		"indexes": map[string]any{
			"projects/" + projectID + "/database/sessions/links": []any{
				"branches/main/projects/" + projectID + "/database/" + kvID},
			"projects/" + projectID + "/database/orders/links": []any{
				"branches/main/projects/" + projectID + "/database/" + sqlID},
			"projects/" + projectID + "/database/ledger/links": []any{
				"branches/main/projects/" + projectID + "/database/" + raftID},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// published is a register holding one deployment, so the service reads real
// declarations rather than being told what they are.
type published struct {
	revision uint64
	object   []byte
}

func (p *published) Resolve(context.Context, string, string) (register.Pointer, error) {
	return register.Pointer{Object: register.IDOf(p.object), Revision: p.revision}, nil
}

func (p *published) Object(_ context.Context, id register.ObjectID) ([]byte, error) {
	if id != register.IDOf(p.object) {
		return nil, fmt.Errorf("no such object")
	}
	return p.object, nil
}

// serving is the service on one host and a client on another, over mocknet.
func serving(t *testing.T) (databasev1connect.DatabaseServiceClient, *Service) {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	server, err := mn.GenPeer()
	require.NoError(t, err)
	caller, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	engine := &counting{}
	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), engine))

	s, err := New(server,
		WithEngines(registry),
		WithRegister(&published{revision: 1, object: compiled()}),
		WithIdle(time.Hour))
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	conn, err := rpc.NewClient(caller, server.ID(), Protocol)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return databasev1connect.NewDatabaseServiceClient(conn.HTTP(), conn.URL()), s
}

func ref(id string) *databasev1.Ref {
	return &databasev1.Ref{Project: "shop", Environment: "prod", Id: id}
}

// TestADatabaseRoundTripsOverTheWire, which is the whole service: a caller
// names a database, the service reads what it is, opens it and answers.
func TestADatabaseRoundTripsOverTheWire(t *testing.T) {
	client, s := serving(t)

	_, err := client.KVPut(t.Context(), connect.NewRequest(&databasev1.KVPutRequest{
		Ref: ref(kvID), Key: "token-1", Value: []byte("alice"),
	}))
	require.NoError(t, err)

	got, err := client.KVGet(t.Context(), connect.NewRequest(&databasev1.KVGetRequest{
		Ref: ref(kvID), Key: "token-1",
	}))
	require.NoError(t, err)
	require.Equal(t, "alice", string(got.Msg.GetValue()))

	require.Equal(t, 1, s.Held(), "the database is warm after the call that opened it")

	// A KEY NOBODY WROTE IS NotFound, not an empty value.
	_, err = client.KVGet(t.Context(), connect.NewRequest(&databasev1.KVGetRequest{
		Ref: ref(kvID), Key: "never-written",
	}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// AND DELETING WHAT IS NOT THERE SUCCEEDS: the caller wanted it gone.
	_, err = client.KVDelete(t.Context(), connect.NewRequest(&databasev1.KVDeleteRequest{
		Ref: ref(kvID), Key: "never-written",
	}))
	require.NoError(t, err)
}

// TestAModelThisServiceDoesNotServeIsRefusedByName. `database` is four models
// on one axis and a consensus on another; this serves one cell and says so
// rather than answering and doing the wrong thing.
func TestAModelThisServiceDoesNotServeIsRefusedByName(t *testing.T) {
	client, _ := serving(t)

	_, err := client.KVGet(t.Context(), connect.NewRequest(&databasev1.KVGetRequest{
		Ref: ref(sqlID), Key: "anything",
	}))
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err),
		"a sql database reached through a kv call is not a caller error to retry")
	require.ErrorContains(t, err, "does not serve that model")
}

// TestAConsensusThisServiceDoesNotKeepIsRefusedByName.
//
// This is the gap the vocabulary has carried since `consensus` was declared:
// RequiredWhen the engine is distributed, and read by nobody, so a system of
// record asking for raft silently got last-writer-wins. node/queue.go refuses a
// crdt queue by name and node/cache.go refuses a cache the same way; the one
// kind the vocabulary calls a system of record had no such check.
func TestAConsensusThisServiceDoesNotKeepIsRefusedByName(t *testing.T) {
	client, _ := serving(t)

	_, err := client.KVPut(t.Context(), connect.NewRequest(&databasev1.KVPutRequest{
		Ref: ref(raftID), Key: "entry", Value: []byte("1"),
	}))
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
	require.ErrorContains(t, err, "does not keep that consensus")
	require.ErrorContains(t, err, "raft", "the refusal names what was asked for")
}

// TestAnIdThisDeploymentDoesNotDeclareIsNotFound, and never an empty answer: a
// caller writing into a database that does not exist would believe it had.
func TestAnIdThisDeploymentDoesNotDeclareIsNotFound(t *testing.T) {
	client, _ := serving(t)

	_, err := client.KVGet(t.Context(), connect.NewRequest(&databasev1.KVGetRequest{
		Ref: ref("QmNobodyDeclaredThis"), Key: "k",
	}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// TestAnAddressWithAHoleInItSaysWhichPart.
func TestAnAddressWithAHoleInItSaysWhichPart(t *testing.T) {
	client, _ := serving(t)

	for _, tc := range []struct {
		name string
		ref  *databasev1.Ref
		says string
	}{
		{"no project", &databasev1.Ref{Environment: "prod", Id: kvID}, "no project"},
		{"no environment", &databasev1.Ref{Project: "shop", Id: kvID}, "no environment"},
		{"no id", &databasev1.Ref{Project: "shop", Environment: "prod"}, "no database id"},
		{"a separator", &databasev1.Ref{Project: "shop", Environment: "prod", Id: "a/b"}, "carries a separator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.KVGet(t.Context(), connect.NewRequest(&databasev1.KVGetRequest{
				Ref: tc.ref, Key: "k",
			}))
			require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
			require.ErrorContains(t, err, tc.says)
		})
	}
}

// TestListPagesByKey, so a page is stable under concurrent writes: a row
// inserted before the cursor cannot shift the window.
func TestListPagesByKey(t *testing.T) {
	client, _ := serving(t)

	for _, key := range []string{"a", "b", "c", "d"} {
		_, err := client.KVPut(t.Context(), connect.NewRequest(&databasev1.KVPutRequest{
			Ref: ref(kvID), Key: key, Value: []byte(key),
		}))
		require.NoError(t, err)
	}

	first, err := client.KVList(t.Context(), connect.NewRequest(&databasev1.KVListRequest{
		Ref: ref(kvID), Limit: 2,
	}))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, first.Msg.GetKeys())
	require.True(t, first.Msg.GetMore())

	next, err := client.KVList(t.Context(), connect.NewRequest(&databasev1.KVListRequest{
		Ref: ref(kvID), Limit: 2, After: "b",
	}))
	require.NoError(t, err)
	require.Equal(t, []string{"c", "d"}, next.Msg.GetKeys())
	require.False(t, next.Msg.GetMore())
}
