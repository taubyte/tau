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

func TestDestroyRemovesOnlyThisDatabase(t *testing.T) {
	logger := logging.Logger("test")
	node := mockPeer(t)
	f := opener{node}

	opened, err := f.New(logger, "databases/shop/prod/kv/QmGoing", 10)
	require.NoError(t, err)

	staying, err := f.New(logger, "databases/shop/prod/kv/QmStaying", 10)
	require.NoError(t, err)

	ctx := t.Context()
	for i, db := range []*kvDatabase{opened, staying} {
		require.NoError(t, db.Put(ctx, "k", []byte{byte(i)}))
	}

	require.NotZero(t, keysUnder(t, node, "databases/shop/prod/kv/QmGoing"))
	before := keysUnder(t, node, "databases/shop/prod/kv/QmStaying")
	require.NotZero(t, before)

	require.NoError(t, opened.Destroy(ctx))

	require.Zero(t, keysUnder(t, node, "databases/shop/prod/kv/QmGoing"),
		"this node still holds a database it let go of")
	require.Equal(t, before, keysUnder(t, node, "databases/shop/prod/kv/QmStaying"),
		"letting go of one database took another's bytes with it")

	again, err := f.New(logger, "databases/shop/prod/kv/QmGoing", 10)
	require.NoError(t, err)
	require.NotSame(t, opened, again)
	again.Close()
	staying.Close()
}
