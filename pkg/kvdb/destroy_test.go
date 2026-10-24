package kvdb

import (
	"context"
	"testing"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	logging "github.com/ipfs/go-log/v2"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/peer"
)

// keysUnder counts what the NODE's own datastore holds for one database, which
// is where a replicated set actually sits — see New, which opens it at
// `crdt/<path>`.
func keysUnder(t *testing.T, node peer.Node, path string) int {
	t.Helper()

	res, err := node.Store().Query(context.Background(), query.Query{
		Prefix: ds.NewKey("crdt/" + path).String(), KeysOnly: true,
	})
	require.NoError(t, err)
	defer res.Close()

	held := 0
	for entry := range res.Next() {
		require.NoError(t, entry.Error)
		held++
	}
	return held
}

// TestDestroyRemovesWhatThisNodeHoldsAndNothingElse.
//
// THIS IS THE ONLY WAY TO STOP BEING ONE OF THE COPIES. Close unsubscribes and
// leaves the bytes on disk; Delete removes a key and broadcasts a tombstone
// every replica applies, so a node letting go would delete the database for the
// whole fleet — including from the node that just took it over. Destroy goes
// UNDER the CRDT: it removes the prefix from this node's own store, which the
// broadcaster never sees.
func TestDestroyRemovesOnlyThisDatabase(t *testing.T) {
	logger := logging.Logger("test")
	node := mockPeer(t)
	f := New(node)

	opened, err := f.New(logger, "databases/shop/prod/kv/QmGoing", 10)
	require.NoError(t, err)

	// DESTROY IS NOT ON core/kvdb.KVDB, deliberately: it is a statement a node
	// makes about bytes it holds, and the shared interface is also implemented
	// by handles that hold none. A consumer that needs it asks for it.
	going, canDestroy := opened.(interface {
		Destroy(context.Context) error
	})
	require.True(t, canDestroy)
	staying, err := f.New(logger, "databases/shop/prod/kv/QmStaying", 10)
	require.NoError(t, err)

	ctx := t.Context()
	for i, db := range []interface {
		Put(context.Context, string, []byte) error
	}{opened, staying} {
		require.NoError(t, db.Put(ctx, "k", []byte{byte(i)}))
	}

	require.NotZero(t, keysUnder(t, node, "databases/shop/prod/kv/QmGoing"))
	before := keysUnder(t, node, "databases/shop/prod/kv/QmStaying")
	require.NotZero(t, before)

	require.NoError(t, going.Destroy(ctx))

	require.Zero(t, keysUnder(t, node, "databases/shop/prod/kv/QmGoing"),
		"this node still holds a database it let go of")
	require.Equal(t, before, keysUnder(t, node, "databases/shop/prod/kv/QmStaying"),
		"letting go of one database took another's bytes with it")

	// AND IT CAN BE TAKEN BACK. Destroy drops the database from the factory, so
	// a later open is a fresh subscription that syncs from whoever still holds
	// it — a node that let go is not a node that refused forever.
	again, err := f.New(logger, "databases/shop/prod/kv/QmGoing", 10)
	require.NoError(t, err)
	require.NotSame(t, opened, again)
	again.Close()
	staying.Close()
}
