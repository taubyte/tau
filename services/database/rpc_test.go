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
	"github.com/taubyte/tau/pkg/fleet"
	databasev2 "github.com/taubyte/tau/pkg/proto/gen/tau/database/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/database/v2/databasev2connect"
	"github.com/taubyte/tau/pkg/register"
)

// Drives the service over a real libp2p stream: what a caller can reach and what it
// is refused.

const (
	projectID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	kvID      = "QmVLDAzEjEbfBPuoyLPWnPMhKp8ceDXwGdmMKY5W3nZWfG"
	sqlID     = "QmSgvGwSJmtqBCJ8Zw3rNKtCCTqTFcqSUQqLYyPBnW6cVs"
	raftID    = "QmZ4gCbmYaFZ4dWJmvNBQKvNAWMYtHqQTHRDprHmxPh6Zk"
	vectorID  = "QmPq9zYJvZ8kLpQ2mWfFPd6Fy1kVYhZLb8vtNJcXAGL3Rd"
)

// compiled is a project with four databases: one this service serves, one of a
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
				vectorID: map[string]any{
					"name": "knowledge", "type": "vector",
					"vector": map[string]any{
						"engine": "distributed", "consensus": "crdt",
						"dimensions": float64(1536), "metric": "cosine",
					},
				},
			},
		},

		// The index makes an id addressable (pkg/specs reads it backwards).
		"indexes": map[string]any{
			"projects/" + projectID + "/database/sessions/links": []any{
				"branches/main/projects/" + projectID + "/database/" + kvID},
			"projects/" + projectID + "/database/orders/links": []any{
				"branches/main/projects/" + projectID + "/database/" + sqlID},
			"projects/" + projectID + "/database/ledger/links": []any{
				"branches/main/projects/" + projectID + "/database/" + raftID},
			"projects/" + projectID + "/database/knowledge/links": []any{
				"branches/main/projects/" + projectID + "/database/" + vectorID},
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

	// fleetDoc is what this cloud declares about itself, or nil for a cloud
	// that has declared nothing — which every node reads as the default.
	fleetDoc []byte
}

func (p *published) Resolve(context.Context, string, string) (register.Pointer, error) {
	return register.Pointer{Object: register.IDOf(p.object), Revision: p.revision}, nil
}

func (p *published) Object(_ context.Context, id register.ObjectID) ([]byte, error) {
	switch id {
	case register.IDOf(p.object):
		return p.object, nil
	case register.IDOf(p.fleetDoc):
		if p.fleetDoc != nil {
			return p.fleetDoc, nil
		}
	}
	return nil, fmt.Errorf("no such object")
}

func (p *published) ResolveFleet(context.Context) (register.Pointer, error) {
	if p.fleetDoc == nil {
		return register.Pointer{}, fmt.Errorf("%w: the fleet", register.ErrNotPublished)
	}
	return register.Pointer{Object: register.IDOf(p.fleetDoc), Revision: 1}, nil
}

// declaringFloor is a cloud that has published a floor.
func declaringFloor(t *testing.T, object []byte, replication int) *published {
	t.Helper()
	doc, err := fleet.Encode(fleet.Fleet{Replication: replication})
	require.NoError(t, err)
	return &published{revision: 1, object: object, fleetDoc: doc}
}

// serving is the service on one host and a client on another, over mocknet.
func serving(t *testing.T) (databasev2connect.DatabaseServiceClient, *Service) {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	server, err := mn.GenPeer()
	require.NoError(t, err)
	caller, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	engine := &accounting{}
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

	return databasev2connect.NewDatabaseServiceClient(conn.HTTP(), conn.URL()), s
}

func ref(id string) *databasev2.Ref {
	return &databasev2.Ref{Project: "shop", Environment: "prod", Id: id}
}

func TestADatabaseRoundTripsOverTheWire(t *testing.T) {
	client, s := serving(t)

	_, err := client.KVPut(t.Context(), connect.NewRequest(&databasev2.KVPutRequest{
		Ref: ref(kvID), Key: "token-1", Value: []byte("alice"),
	}))
	require.NoError(t, err)

	got, err := client.KVGet(t.Context(), connect.NewRequest(&databasev2.KVGetRequest{
		Ref: ref(kvID), Key: "token-1",
	}))
	require.NoError(t, err)
	require.Equal(t, "alice", string(got.Msg.GetValue()))

	require.Equal(t, 1, s.Held(), "the database is warm after the call that opened it")

	// A KEY NOBODY WROTE IS NotFound, not an empty value.
	_, err = client.KVGet(t.Context(), connect.NewRequest(&databasev2.KVGetRequest{
		Ref: ref(kvID), Key: "never-written",
	}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// AND DELETING WHAT IS NOT THERE SUCCEEDS: the caller wanted it gone.
	_, err = client.KVDelete(t.Context(), connect.NewRequest(&databasev2.KVDeleteRequest{
		Ref: ref(kvID), Key: "never-written",
	}))
	require.NoError(t, err)
}

func TestAModelThisServiceDoesNotServeIsRefusedByName(t *testing.T) {
	client, _ := serving(t)

	_, err := client.KVGet(t.Context(), connect.NewRequest(&databasev2.KVGetRequest{
		Ref: ref(sqlID), Key: "anything",
	}))
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err),
		"a sql database reached through a kv call is not a caller error to retry")
	require.ErrorContains(t, err, "does not serve that model")
}

func TestANodeThatCannotKeepAConsensusRefusesByName(t *testing.T) {
	client, _ := serving(t)

	_, err := client.KVPut(t.Context(), connect.NewRequest(&databasev2.KVPutRequest{
		Ref: ref(raftID), Key: "entry", Value: []byte("1"),
	}))
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
	require.ErrorContains(t, err, "does not keep that consensus")
	require.ErrorContains(t, err, "raft", "the refusal names what was asked for")
}

func TestAnIdThisDeploymentDoesNotDeclareIsNotFound(t *testing.T) {
	client, _ := serving(t)

	_, err := client.KVGet(t.Context(), connect.NewRequest(&databasev2.KVGetRequest{
		Ref: ref("QmNobodyDeclaredThis"), Key: "k",
	}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestAnAddressWithAHoleInItSaysWhichPart(t *testing.T) {
	client, _ := serving(t)

	for _, tc := range []struct {
		name string
		ref  *databasev2.Ref
		says string
	}{
		{"no project", &databasev2.Ref{Environment: "prod", Id: kvID}, "no project"},
		{"no environment", &databasev2.Ref{Project: "shop", Id: kvID}, "no environment"},
		{"no id", &databasev2.Ref{Project: "shop", Environment: "prod"}, "no database id"},
		{"a separator", &databasev2.Ref{Project: "shop", Environment: "prod", Id: "a/b"}, "carries a separator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.KVGet(t.Context(), connect.NewRequest(&databasev2.KVGetRequest{
				Ref: tc.ref, Key: "k",
			}))
			require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
			require.ErrorContains(t, err, tc.says)
		})
	}
}

func TestListPagesByKey(t *testing.T) {
	client, _ := serving(t)

	for _, key := range []string{"a", "b", "c", "d"} {
		_, err := client.KVPut(t.Context(), connect.NewRequest(&databasev2.KVPutRequest{
			Ref: ref(kvID), Key: key, Value: []byte(key),
		}))
		require.NoError(t, err)
	}

	first, err := client.KVList(t.Context(), connect.NewRequest(&databasev2.KVListRequest{
		Ref: ref(kvID), Limit: 2,
	}))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, first.Msg.GetKeys())
	require.True(t, first.Msg.GetMore())

	next, err := client.KVList(t.Context(), connect.NewRequest(&databasev2.KVListRequest{
		Ref: ref(kvID), Limit: 2, After: "b",
	}))
	require.NoError(t, err)
	require.Equal(t, []string{"c", "d"}, next.Msg.GetKeys())
	require.False(t, next.Msg.GetMore())
}
