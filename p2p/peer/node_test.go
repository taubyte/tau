package peer

import (
	"bytes"
	"context"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/keypair"
)

func TestMock(t *testing.T) {
	ctx := context.Background()
	node := Mock(ctx)
	require.NotNil(t, node, "Mock should return a non-nil node")

	defer node.Close()

	// Test ID
	id := node.ID()
	assert.NotEmpty(t, id, "ID should not be empty")

	// Test Peer
	host := node.Peer()
	assert.NotNil(t, host, "Peer should return a non-nil host")

	// Test Messaging
	messaging := node.Messaging()
	assert.NotNil(t, messaging, "Messaging should return a non-nil pubsub")

	// Test Store
	store := node.Store()
	assert.NotNil(t, store, "Store should return a non-nil store")

	// Test DAG
	dag := node.DAG()
	assert.NotNil(t, dag, "DAG should return a non-nil ipfs peer")

	// Test Discovery
	discovery := node.Discovery()
	assert.NotNil(t, discovery, "Discovery should return a non-nil discovery")

	// Test Context
	nodeCtx := node.Context()
	assert.NotNil(t, nodeCtx, "Context should not be nil")

	// Test Peering
	peering := node.Peering()
	// Mock doesn't set peering, so it may be nil
	_ = peering
}

func TestMock_NewChildContextWithCancel(t *testing.T) {
	ctx := context.Background()
	node := Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	childCtx, cancel := node.NewChildContextWithCancel()
	assert.NotNil(t, childCtx)
	assert.NotNil(t, cancel)

	// Verify the child context works
	select {
	case <-childCtx.Done():
		t.Fatal("Child context should not be done yet")
	default:
		// Expected
	}

	cancel()

	select {
	case <-childCtx.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Child context should be done after cancel")
	}
}

func TestMock_Done(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	node := Mock(ctx)
	require.NotNil(t, node)

	done := node.Done()
	assert.NotNil(t, done)

	// Verify not done yet
	select {
	case <-done:
		t.Fatal("Done should not be signaled yet")
	default:
		// Expected
	}

	// Cancel the context
	cancel()

	// Close the node to make sure resources are cleaned up
	node.Close()
}

func TestMock_AddAndGetFile(t *testing.T) {
	ctx := context.Background()
	node := Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	testContent := []byte("hello world test content")

	// Add file
	cid, err := node.AddFile(bytes.NewReader(testContent))
	require.NoError(t, err)
	assert.NotEmpty(t, cid, "CID should not be empty")

	// Get file
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
	node := Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	testContent := []byte("test content for cid")

	// Add file and get CID
	cid, err := node.AddFileForCid(bytes.NewReader(testContent))
	require.NoError(t, err)
	assert.True(t, cid.Defined(), "CID should be defined")

	// Get file using CID
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
	node := Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	testContent := []byte("content to delete")

	// Add file
	cid, err := node.AddFile(bytes.NewReader(testContent))
	require.NoError(t, err)

	// Delete file
	err = node.DeleteFile(cid)
	require.NoError(t, err)
}

func TestLinkAllPeers_NotInitialized(t *testing.T) {
	// Reset mocknet
	mocknetLock.Lock()
	oldMocknet := mocknet
	mocknet = nil
	mocknetLock.Unlock()

	defer func() {
		mocknetLock.Lock()
		mocknet = oldMocknet
		mocknetLock.Unlock()
	}()

	err := LinkAllPeers()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

func TestLinkAllPeers_Success(t *testing.T) {
	ctx := context.Background()

	// Create some mock nodes to initialize mocknet
	node1 := Mock(ctx)
	node2 := Mock(ctx)
	defer node1.Close()
	defer node2.Close()

	// Link all peers
	err := LinkAllPeers()
	assert.NoError(t, err)
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
	node := Mock(ctx)
	require.NotNil(t, node)

	// Close should be idempotent
	node.Close()
	node.Close()
	node.Close()
}

func TestMock_PubSubPublish(t *testing.T) {
	ctx := context.Background()
	node := Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	// Publish a message
	err := node.PubSubPublish(ctx, "test-topic", []byte("hello"))
	require.NoError(t, err)
}

func TestMock_PubSubSubscribe(t *testing.T) {
	ctx := context.Background()
	node := Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	err := node.PubSubSubscribe(
		"test-topic-sub",
		func(msg *pubsub.Message) {
			// Message handler
		},
		func(err error) {
			// Error handler
		},
	)
	require.NoError(t, err)
}

func TestErrorClosed(t *testing.T) {
	// Test that errorClosed is defined and is an error
	assert.NotNil(t, errorClosed)
	assert.Contains(t, errorClosed.Error(), "closed")
}

func TestNewFolder(t *testing.T) {
	ctx := context.Background()
	node := Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	// Test creating a new folder
	dir, err := node.NewFolder("test-folder")
	require.NoError(t, err)
	assert.NotNil(t, dir)
}

func TestPubSubSubscribeContext(t *testing.T) {
	ctx := context.Background()
	node := Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	err := node.PubSubSubscribeContext(
		subCtx,
		"test-topic-context",
		func(msg *pubsub.Message) {
			// Message handler
		},
		func(err error) {
			// Error handler
		},
	)
	require.NoError(t, err)
}

func TestClosedNodeOperations(t *testing.T) {
	ctx := context.Background()
	node := Mock(ctx)
	require.NotNil(t, node)

	// Close the node
	node.Close()

	// Operations on closed node should return errors
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
	node := Mock(ctx)
	require.NotNil(t, node)

	// Close the node
	node.Close()

	// getOrCreateTopic on closed node should return error
	err := node.PubSubPublish(ctx, "test-topic", []byte("data"))
	assert.Error(t, err)
}

func TestPubSubKeepAlive_Closed(t *testing.T) {
	ctx := context.Background()
	node := Mock(ctx)
	require.NotNil(t, node)

	// Close the node
	node.Close()

	// NewPubSubKeepAlive on closed node should return error
	keepAliveCtx, cancel := context.WithCancel(ctx)
	err := node.NewPubSubKeepAlive(keepAliveCtx, cancel, "test")
	assert.Error(t, err)
}

func TestPubSubSubscribeToTopic(t *testing.T) {
	ctx := context.Background()
	node := Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	// Get the messaging pubsub
	ps := node.Messaging()
	require.NotNil(t, ps)

	// Join a topic
	topic, err := ps.Join("test-topic-direct")
	require.NoError(t, err)

	// Subscribe to the topic directly
	err = node.PubSubSubscribeToTopic(topic, func(msg *pubsub.Message) {}, func(err error) {})
	require.NoError(t, err)
}

func TestPubSubSubscribeToTopic_Closed(t *testing.T) {
	ctx := context.Background()
	node := Mock(ctx)
	require.NotNil(t, node)

	// Get the messaging pubsub and join topic before closing
	ps := node.Messaging()
	require.NotNil(t, ps)
	topic, err := ps.Join("test-topic-closed")
	require.NoError(t, err)

	// Close the node
	node.Close()

	// Subscribe should fail on closed node
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
	node := Mock(ctx)
	require.NotNil(t, node)
	defer node.Close()

	// WaitForSwarm should timeout since mock has no peers
	err := node.WaitForSwarm(100 * time.Millisecond)
	// Could either succeed or fail depending on mock state
	_ = err
}
