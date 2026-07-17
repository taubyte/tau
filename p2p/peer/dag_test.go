package peer

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAG_Accessors(t *testing.T) {
	ctx := context.Background()
	node := Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	dag := node.DAG()
	require.NotNil(t, dag)

	assert.NotNil(t, dag.BlockStore(), "BlockStore should be non-nil")
	assert.NotNil(t, dag.BlockService(), "BlockService should be non-nil")
	assert.NotNil(t, dag.Exchange(), "Exchange should be non-nil")
	assert.NotNil(t, dag.Session(ctx), "Session should be non-nil")
}

func TestDAG_HasBlock(t *testing.T) {
	ctx := context.Background()
	node := Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	dag := node.DAG()

	c, err := node.AddFileForCid(bytes.NewReader([]byte("has block content")))
	require.NoError(t, err)

	// Present locally after add.
	has, err := dag.HasBlock(ctx, c)
	require.NoError(t, err)
	assert.True(t, has, "added block should be present")

	// Same answer via the blockstore accessor (mirrors dfs backend usage).
	has, err = dag.BlockStore().Has(ctx, c)
	require.NoError(t, err)
	assert.True(t, has)
}

func TestDAG_CloseIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	node := Mock(ctx)
	require.NotNil(t, node)

	dag := node.DAG()
	require.NoError(t, dag.Close())
	require.NoError(t, dag.Close(), "Close must be idempotent")

	cancel()
	node.Close()
}

func TestDefaultBootstrapPeers(t *testing.T) {
	peers := DefaultBootstrapPeers()
	assert.NotEmpty(t, peers, "should return the public IPFS bootstrap peers")
}
