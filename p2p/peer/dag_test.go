package peer

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	blockstore "github.com/ipfs/boxo/blockstore"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	dssync "github.com/ipfs/go-datastore/sync"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAG_Accessors(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
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
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	dag := node.DAG()

	c, err := node.AddFileForCid(bytes.NewReader([]byte("has block content")))
	require.NoError(t, err)

	has, err := dag.HasBlock(ctx, c)
	require.NoError(t, err)
	assert.True(t, has, "added block should be present")

	has, err = dag.BlockStore().Has(ctx, c)
	require.NoError(t, err)
	assert.True(t, has)
}

func TestDAG_ReadBlock(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	dag := node.DAG()
	held := blocks.NewBlock([]byte("read block content"))
	require.NoError(t, dag.WriteBlock(ctx, held))

	b, err := dag.ReadBlock(ctx, held.Cid())
	require.NoError(t, err)
	assert.Equal(t, held.Cid(), b.Cid())
	assert.Equal(t, held.RawData(), b.RawData())

	// A block not held is asked of the exchange, which no peer answers.
	fetch, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = dag.ReadBlock(fetch, blocks.NewBlock([]byte("not held")).Cid())
	assert.Error(t, err)
}

func TestDAG_BlockLogIsTheNodesStore(t *testing.T) {
	node := testNet(t).Mock(context.Background())
	require.NotNil(t, node)
	defer node.Close()
	assert.True(t, node.DAG().BlockLog() == node.Store())
}

func TestDAG_CloseIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	node := testNet(t).Mock(ctx)
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

// lateQueryStore holds each block query until its context ends and records
// one that went on to read a closed store, where pebble panics.
type lateQueryStore struct {
	datastore.Batching
	closed atomic.Bool
	late   atomic.Bool
}

func (s *lateQueryStore) Query(ctx context.Context, q query.Query) (query.Results, error) {
	if !strings.HasPrefix(q.Prefix, blockstore.BlockPrefix.String()) {
		return s.Batching.Query(ctx, q)
	}
	<-ctx.Done()
	if s.closed.Load() {
		s.late.Store(true)
	}
	return s.Batching.Query(ctx, q)
}

func (s *lateQueryStore) Close() error {
	s.closed.Store(true)
	return s.Batching.Close()
}

func TestDAG_CloseWaitsForTheBloomBuild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := testNet(t).Mock(ctx).(*node)
	defer n.Close()

	store := &lateQueryStore{Batching: dssync.MutexWrap(datastore.NewMapDatastore())}
	dag, err := newDAG(ctx, store, n.host, n.dht)
	require.NoError(t, err)

	require.NoError(t, dag.Close())
	require.NoError(t, store.Close())
	cancel()
	dag.bstore.(bloomBuild).Wait(context.Background())
	assert.False(t, store.late.Load(), "the bloom build read the store after it was closed")
}
