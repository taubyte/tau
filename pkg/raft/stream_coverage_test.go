package raft

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"testing"
	"time"

	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"
)

// TestClient_Set_Success tests Set with a separate client node connecting to cluster node
func TestClient_Set_Success(t *testing.T) {
	// Create cluster node
	clusterNode := newTestNode(t)
	clusterNodeInfo := peercore.AddrInfo{ID: clusterNode.ID(), Addrs: clusterNode.Peer().Addrs()}

	// Create separate client node (not part of cluster)
	clientNode := newTestNode(t, clusterNodeInfo)

	// Wait for peers to connect
	time.Sleep(2 * time.Second)

	namespace := "test-client-set"

	// Create cluster on first node
	cl, err := New(clusterNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Create client on separate node
	client, err := NewClient(clientNode, namespace, nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Test Set - client node sends to cluster node
	err = client.Set("test-key", []byte("test-value"), 5*time.Second, clusterNode.ID())
	require.NoError(t, err, "Set should succeed")

	// Verify it was set on cluster
	val, found := cl.Get("test-key")
	require.True(t, found, "key should exist after Set")
	assert.Equal(t, string(val), "test-value")
}

// TestClient_Get_Success tests Get with a separate client node
func TestClient_Get_Success(t *testing.T) {
	clusterNode := newTestNode(t)
	clusterNodeInfo := peercore.AddrInfo{ID: clusterNode.ID(), Addrs: clusterNode.Peer().Addrs()}

	clientNode := newTestNode(t, clusterNodeInfo)
	time.Sleep(2 * time.Second)

	namespace := "test-client-get"

	cl, err := New(clusterNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set a value first on cluster
	require.NoError(t, cl.Set("get-key", []byte("get-value"), time.Second))

	// Create client on separate node
	client, err := NewClient(clientNode, namespace, nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Test Get - client node gets from cluster node
	val, found, err := client.Get("get-key", 0, clusterNode.ID())
	require.NoError(t, err, "Get should succeed")
	require.True(t, found, "key should be found")
	assert.Equal(t, string(val), "get-value")
}

// TestClient_Delete_Success tests Delete with a separate client node
func TestClient_Delete_Success(t *testing.T) {
	clusterNode := newTestNode(t)
	clusterNodeInfo := peercore.AddrInfo{ID: clusterNode.ID(), Addrs: clusterNode.Peer().Addrs()}

	clientNode := newTestNode(t, clusterNodeInfo)
	time.Sleep(2 * time.Second)

	namespace := "test-client-delete"

	cl, err := New(clusterNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set a value first
	require.NoError(t, cl.Set("delete-key", []byte("delete-value"), time.Second))

	// Create client on separate node
	client, err := NewClient(clientNode, namespace, nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Test Delete - client node deletes from cluster node
	err = client.Delete("delete-key", 5*time.Second, clusterNode.ID())
	require.NoError(t, err, "Delete should succeed")

	// Verify it was deleted
	_, found := cl.Get("delete-key")
	require.False(t, found, "key should be deleted")
}

// TestClient_Keys_Success tests Keys with a separate client node
func TestClient_Keys_Success(t *testing.T) {
	clusterNode := newTestNode(t)
	clusterNodeInfo := peercore.AddrInfo{ID: clusterNode.ID(), Addrs: clusterNode.Peer().Addrs()}

	clientNode := newTestNode(t, clusterNodeInfo)
	time.Sleep(2 * time.Second)

	namespace := "test-client-keys"

	cl, err := New(clusterNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set multiple values
	require.NoError(t, cl.Set("keys/a", []byte("a"), time.Second))
	require.NoError(t, cl.Set("keys/b", []byte("b"), time.Second))
	require.NoError(t, cl.Set("keys/c", []byte("c"), time.Second))

	// Create client on separate node
	client, err := NewClient(clientNode, namespace, nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Test Keys - client node gets keys from cluster node
	keys, err := client.Keys("keys/", clusterNode.ID())
	require.NoError(t, err, "Keys should succeed")
	require.GreaterOrEqual(t, len(keys), 3, "should find at least 3 keys")
}

// TestClient_ExchangePeers_Success tests ExchangePeers with a separate client node
func TestClient_ExchangePeers_Success(t *testing.T) {
	clusterNode := newTestNode(t)
	clusterNodeInfo := peercore.AddrInfo{ID: clusterNode.ID(), Addrs: clusterNode.Peer().Addrs()}

	clientNode := newTestNode(t, clusterNodeInfo)
	time.Sleep(2 * time.Second)

	namespace := "test-client-exchange"

	cl, err := New(clusterNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Create client on separate node
	client, err := newInternalClient(clientNode, namespace, nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	ourStart := time.Now()
	ourPeers := map[string]int64{
		clientNode.ID().String(): 0,
	}

	// ExchangePeers - client node exchanges with cluster node
	theirStart, theirPeers, err := client.ExchangePeers(ourStart, ourPeers, clusterNode.ID())
	require.NoError(t, err, "ExchangePeers should succeed")
	require.False(t, theirStart.IsZero(), "theirStart should not be zero")
	require.NotNil(t, theirPeers, "theirPeers should not be nil")
}

// TestClient_JoinVoter_Success tests JoinVoter with a separate client node
func TestClient_JoinVoter_Success(t *testing.T) {
	clusterNode := newTestNode(t)
	clusterNodeInfo := peercore.AddrInfo{ID: clusterNode.ID(), Addrs: clusterNode.Peer().Addrs()}

	clientNode := newTestNode(t, clusterNodeInfo)
	time.Sleep(2 * time.Second)

	namespace := "test-client-join"

	cl, err := New(clusterNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Create client on separate node
	client, err := newInternalClient(clientNode, namespace, nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// JoinVoter - client node joins cluster as voter
	err = client.JoinVoter(clientNode.ID(), 5*time.Second, clusterNode.ID())
	require.NoError(t, err, "JoinVoter should succeed")
}

// TestClient_Set_WithEncryption_Success tests Set with encryption using separate nodes
func TestClient_Set_WithEncryption_Success(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	clusterNode := newTestNode(t)
	clusterNodeInfo := peercore.AddrInfo{ID: clusterNode.ID(), Addrs: clusterNode.Peer().Addrs()}

	clientNode := newTestNode(t, clusterNodeInfo)
	time.Sleep(2 * time.Second)

	namespace := "test-client-enc-set"

	cl, err := New(clusterNode, namespace, WithEncryptionKey(key), WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create encrypted cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Create encrypted client on separate node
	block, err := aes.NewCipher(key)
	require.NoError(t, err, "failed to create cipher")
	gcm, err := cipher.NewGCMWithNonceSize(block, nonceSize)
	require.NoError(t, err, "failed to create GCM")
	client, err := NewClient(clientNode, namespace, gcm)
	require.NoError(t, err, "failed to create encrypted client")
	defer client.Close()

	// Set with encryption
	err = client.Set("enc-set-key", []byte("enc-set-value"), 5*time.Second, clusterNode.ID())
	require.NoError(t, err, "Set with encryption should succeed")

	// Verify it was set
	val, found := cl.Get("enc-set-key")
	require.True(t, found, "key should exist after encrypted Set")
	assert.Equal(t, string(val), "enc-set-value")
}

// TestClient_Get_WithEncryption_Success tests Get with encryption using separate nodes
func TestClient_Get_WithEncryption_Success(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	clusterNode := newTestNode(t)
	clusterNodeInfo := peercore.AddrInfo{ID: clusterNode.ID(), Addrs: clusterNode.Peer().Addrs()}

	clientNode := newTestNode(t, clusterNodeInfo)
	time.Sleep(2 * time.Second)

	namespace := "test-client-enc-get"

	cl, err := New(clusterNode, namespace, WithEncryptionKey(key), WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create encrypted cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set a value first
	require.NoError(t, cl.Set("enc-get-key", []byte("enc-get-value"), time.Second))

	// Create encrypted client on separate node
	block, err := aes.NewCipher(key)
	require.NoError(t, err, "failed to create cipher")
	gcm, err := cipher.NewGCMWithNonceSize(block, nonceSize)
	require.NoError(t, err, "failed to create GCM")
	client, err := NewClient(clientNode, namespace, gcm)
	require.NoError(t, err, "failed to create encrypted client")
	defer client.Close()

	// Get with encryption
	val, found, err := client.Get("enc-get-key", 0, clusterNode.ID())
	require.NoError(t, err, "Get with encryption should succeed")
	require.True(t, found, "key should be found")
	assert.Equal(t, string(val), "enc-get-value")
}

// TestClient_Delete_WithEncryption_Success tests Delete with encryption using separate nodes
func TestClient_Delete_WithEncryption_Success(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	clusterNode := newTestNode(t)
	clusterNodeInfo := peercore.AddrInfo{ID: clusterNode.ID(), Addrs: clusterNode.Peer().Addrs()}

	clientNode := newTestNode(t, clusterNodeInfo)
	time.Sleep(2 * time.Second)

	namespace := "test-client-enc-del"

	cl, err := New(clusterNode, namespace, WithEncryptionKey(key), WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create encrypted cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set a value first
	require.NoError(t, cl.Set("enc-del-key", []byte("enc-del-value"), time.Second))

	// Create encrypted client on separate node
	block, err := aes.NewCipher(key)
	require.NoError(t, err, "failed to create cipher")
	gcm, err := cipher.NewGCMWithNonceSize(block, nonceSize)
	require.NoError(t, err, "failed to create GCM")
	client, err := NewClient(clientNode, namespace, gcm)
	require.NoError(t, err, "failed to create encrypted client")
	defer client.Close()

	// Delete with encryption
	err = client.Delete("enc-del-key", 5*time.Second, clusterNode.ID())
	require.NoError(t, err, "Delete with encryption should succeed")

	// Verify it was deleted
	_, found := cl.Get("enc-del-key")
	require.False(t, found, "key should be deleted")
}

// TestClient_Keys_WithEncryption_Success tests Keys with encryption using separate nodes
func TestClient_Keys_WithEncryption_Success(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	clusterNode := newTestNode(t)
	clusterNodeInfo := peercore.AddrInfo{ID: clusterNode.ID(), Addrs: clusterNode.Peer().Addrs()}

	clientNode := newTestNode(t, clusterNodeInfo)
	time.Sleep(2 * time.Second)

	namespace := "test-client-enc-keys"

	cl, err := New(clusterNode, namespace, WithEncryptionKey(key), WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create encrypted cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set multiple values
	require.NoError(t, cl.Set("enc-keys/a", []byte("a"), time.Second))
	require.NoError(t, cl.Set("enc-keys/b", []byte("b"), time.Second))

	// Create encrypted client on separate node
	block, err := aes.NewCipher(key)
	require.NoError(t, err, "failed to create cipher")
	gcm, err := cipher.NewGCMWithNonceSize(block, nonceSize)
	require.NoError(t, err, "failed to create GCM")
	client, err := NewClient(clientNode, namespace, gcm)
	require.NoError(t, err, "failed to create encrypted client")
	defer client.Close()

	// Keys with encryption
	keys, err := client.Keys("enc-keys/", clusterNode.ID())
	require.NoError(t, err, "Keys with encryption should succeed")
	require.NotNil(t, keys, "keys should not be nil")
	require.GreaterOrEqual(t, len(keys), 2, "should find at least 2 keys")
}
