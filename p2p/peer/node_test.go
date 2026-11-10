package peer

import (
	"bytes"
	"context"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/keypair"
)

func TestMock(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node, "Mock should return a non-nil node")

	defer node.Close()

	id := node.ID()
	assert.NotEmpty(t, id, "ID should not be empty")

	host := node.Host()
	assert.NotNil(t, host, "Peer should return a non-nil host")

	messaging := node.Messaging()
	assert.NotNil(t, messaging, "Messaging should return a non-nil pubsub")

	store := node.Store()
	assert.NotNil(t, store, "Store should return a non-nil store")

	dag := node.DAG()
	assert.NotNil(t, dag, "DAG should return a non-nil ipfs peer")

	discovery := node.Discovery()
	assert.NotNil(t, discovery, "Discovery should return a non-nil discovery")

	nodeCtx := node.Context()
	assert.NotNil(t, nodeCtx, "Context should not be nil")

	node.Peering()
}

func TestMock_NewChildContextWithCancel(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	childCtx, cancel := node.NewChildContextWithCancel()
	assert.NotNil(t, childCtx)
	assert.NotNil(t, cancel)

	select {
	case <-childCtx.Done():
		t.Fatal("Child context should not be done yet")
	default:
	}

	cancel()

	select {
	case <-childCtx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Child context should be done after cancel")
	}
}

func TestMock_Done(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)

	done := node.Done()
	assert.NotNil(t, done)

	select {
	case <-done:
		t.Fatal("Done should not be signaled yet")
	default:
	}

	cancel()

	node.Close()
}

func TestMock_AddAndGetFile(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	testContent := []byte("hello world test content")

	cid, err := node.AddFile(bytes.NewReader(testContent))
	require.NoError(t, err)
	assert.NotEmpty(t, cid, "CID should not be empty")

	reader, err := node.GetFile(ctx, cid)
	require.NoError(t, err)
	defer reader.Close()

	var buf bytes.Buffer
	_, err = reader.WriteTo(&buf)
	require.NoError(t, err)

	assert.Equal(t, testContent, buf.Bytes())
}

func TestMock_AddFileForCid(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	testContent := []byte("test content for cid")

	cid, err := node.AddFileForCid(bytes.NewReader(testContent))
	require.NoError(t, err)
	assert.True(t, cid.Defined(), "CID should be defined")

	reader, err := node.GetFileFromCid(ctx, cid)
	require.NoError(t, err)
	defer reader.Close()

	var buf bytes.Buffer
	_, err = reader.WriteTo(&buf)
	require.NoError(t, err)

	assert.Equal(t, testContent, buf.Bytes())
}

func TestMock_DeleteFile(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	testContent := []byte("content to delete")

	cid, err := node.AddFile(bytes.NewReader(testContent))
	require.NoError(t, err)

	err = node.DeleteFile(cid)
	require.NoError(t, err)
}

// testNet is a network of the test's own, closed when it ends.
func testNet(t testing.TB) *Mocknet {
	m := NewMocknet()
	t.Cleanup(func() { m.Close() })
	return m
}

func TestLinkAll_Success(t *testing.T) {
	ctx := context.Background()
	net := testNet(t)

	node1 := net.Mock(ctx)
	node2 := net.Mock(ctx)
	defer node1.Close()
	defer node2.Close()

	err := net.LinkAll()
	assert.NoError(t, err)
}

func TestLinkAll_LinksEachPairOfOpenNodesOnce(t *testing.T) {
	net := testNet(t)
	a := net.Mock(t.Context())
	defer a.Close()
	b := net.Mock(t.Context())
	defer b.Close()
	c := net.Mock(t.Context())

	require.NoError(t, net.LinkAll())
	require.NoError(t, net.LinkAll())
	assert.Len(t, net.net.LinksBetweenPeers(a.ID(), b.ID()), 1)
	assert.Len(t, net.net.LinksBetweenPeers(a.ID(), c.ID()), 1)
	assert.Empty(t, net.net.LinksBetweenPeers(a.ID(), a.ID()))

	c.Close()
	require.NoError(t, net.LinkAll())
	assert.Empty(t, net.net.LinksBetweenPeers(a.ID(), c.ID()), "a closed node is still linked")
	assert.Len(t, net.net.LinksBetweenPeers(a.ID(), b.ID()), 1)
}

// Nodes on two networks never reach each other.
func TestTwoNetworksDoNotMeet(t *testing.T) {
	a := testNet(t).Mock(t.Context())
	defer a.Close()
	b := testNet(t).Mock(t.Context())
	defer b.Close()
	require.Error(t, a.Host().Connect(t.Context(), peercore.AddrInfo{ID: b.ID(), Addrs: b.Host().Addrs()}))
}

func TestStandAlone(t *testing.T) {
	bp := StandAlone()
	assert.False(t, bp.Enable)
	assert.Empty(t, bp.Peers)
}

func TestBootstrap(t *testing.T) {
	bp := Bootstrap()
	assert.True(t, bp.Enable)
	assert.Empty(t, bp.Peers)
}

func TestClose_Multiple(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)

	node.Close()
	node.Close()
	node.Close()
}

func TestMock_PubSubPublish(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	err := node.PubSubPublish(ctx, "test-topic", []byte("hello"))
	require.NoError(t, err)
}

func TestMock_PubSubSubscribe(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	err := node.PubSubSubscribe(
		"test-topic-sub",
		func(msg *pubsub.Message) {
		},
		func(err error) {
		},
	)
	require.NoError(t, err)
}

func TestErrorClosed(t *testing.T) {
	assert.NotNil(t, errorClosed)
	assert.Contains(t, errorClosed.Error(), "closed")
}

func TestNewFolder(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	dir, err := node.NewFolder("test-folder")
	require.NoError(t, err)
	assert.NotNil(t, dir)
}

func TestPubSubSubscribeContext(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	err := node.PubSubSubscribeContext(
		subCtx,
		"test-topic-context",
		func(msg *pubsub.Message) {
		},
		func(err error) {
		},
	)
	require.NoError(t, err)
}

func TestClosedNodeOperations(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)

	node.Close()

	err := node.PubSubPublish(ctx, "test", []byte("data"))
	assert.Error(t, err)

	err = node.PubSubSubscribe("test", func(msg *pubsub.Message) {}, func(err error) {})
	assert.Error(t, err)

	_, err = node.AddFile(bytes.NewReader([]byte("test")))
	assert.Error(t, err)

	err = node.DeleteFile("somecid")
	assert.Error(t, err)

	_, err = node.GetFile(ctx, "somecid")
	assert.Error(t, err)

	_, err = node.AddFileForCid(bytes.NewReader([]byte("test")))
	assert.Error(t, err)
}

func TestGetOrCreateTopic_Closed(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)

	node.Close()

	err := node.PubSubPublish(ctx, "test-topic", []byte("data"))
	assert.Error(t, err)
}

func TestPubSubKeepAlive_Closed(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)

	node.Close()

	keepAliveCtx, cancel := context.WithCancel(ctx)
	err := node.NewPubSubKeepAlive(keepAliveCtx, cancel, "test")
	assert.Error(t, err)
}

func TestPubSubSubscribeToTopic(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	ps := node.Messaging()
	require.NotNil(t, ps)

	topic, err := ps.Join("test-topic-direct")
	require.NoError(t, err)

	err = node.PubSubSubscribeToTopic(topic, func(msg *pubsub.Message) {}, func(err error) {})
	require.NoError(t, err)
}

func TestPubSubSubscribeToTopic_Closed(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)

	ps := node.Messaging()
	require.NotNil(t, ps)
	topic, err := ps.Join("test-topic-closed")
	require.NoError(t, err)

	node.Close()

	err = node.PubSubSubscribeToTopic(topic, func(msg *pubsub.Message) {}, func(err error) {})
	assert.Error(t, err)
}

func TestNewFull(t *testing.T) {
	ctx := context.Background()

	node, err := NewFull(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/16001"},
		nil,
		true,
		Bootstrap(),
	)
	require.NoError(t, err)
	require.NotNil(t, node)
	defer node.Close()

	assert.NotEmpty(t, node.ID())
}

func TestNewPublic(t *testing.T) {
	ctx := context.Background()

	node, err := NewPublic(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/16002"},
		nil,
		StandAlone(),
	)
	require.NoError(t, err)
	require.NotNil(t, node)
	defer node.Close()

	assert.NotEmpty(t, node.ID())
}

func TestNewLitePublic(t *testing.T) {
	ctx := context.Background()

	node, err := NewLitePublic(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/16003"},
		nil,
		StandAlone(),
	)
	require.NoError(t, err)
	require.NotNil(t, node)
	defer node.Close()

	assert.NotEmpty(t, node.ID())
}

func TestNewClientNode(t *testing.T) {
	ctx := context.Background()

	node, err := NewClientNode(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/16004"},
		nil,
		true,
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, node)
	defer node.Close()

	assert.NotEmpty(t, node.ID())
}

func TestNewWithBootstrapList(t *testing.T) {
	ctx := context.Background()

	node, err := NewWithBootstrapList(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/16005"},
		nil,
		true,
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, node)
	defer node.Close()

	assert.NotEmpty(t, node.ID())
}

func TestWaitForSwarm_Timeout(t *testing.T) {
	ctx := context.Background()
	node := testNet(t).Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	node.WaitForSwarm(100 * time.Millisecond)
}
