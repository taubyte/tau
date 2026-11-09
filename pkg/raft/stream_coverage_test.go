//go:build raft_integration

package raft

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"
)

func TestClient_Set_Success_Integration(t *testing.T) {
	clusterNode := newTestNode(t)

	clientNode := newTestNode(t)

	namespace := "test-client-set"

	cl, err := New(clusterNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")
	linkTo(t, clientNode, clusterNode)

	client, err := NewClient(clientNode, namespace, nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	err = client.Set("test-key", []byte("test-value"), 5*time.Second, clusterNode.ID())
	require.NoError(t, err, "Set should succeed")

	val, found := cl.Get("test-key")
	require.True(t, found, "key should exist after Set")
	assert.Equal(t, string(val), "test-value")
}

func TestClient_Get_Success_Integration(t *testing.T) {
	clusterNode := newTestNode(t)
	clientNode := newTestNode(t)

	namespace := "test-client-get"

	cl, err := New(clusterNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")
	linkTo(t, clientNode, clusterNode)

	require.NoError(t, cl.Set("get-key", []byte("get-value"), time.Second))

	client, err := NewClient(clientNode, namespace, nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	val, found, err := client.Get("get-key", 0, clusterNode.ID())
	require.NoError(t, err, "Get should succeed")
	require.True(t, found, "key should be found")
	assert.Equal(t, string(val), "get-value")
}

func TestClient_Delete_Success_Integration(t *testing.T) {
	clusterNode := newTestNode(t)
	clientNode := newTestNode(t)

	namespace := "test-client-delete"

	cl, err := New(clusterNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")
	linkTo(t, clientNode, clusterNode)

	require.NoError(t, cl.Set("delete-key", []byte("delete-value"), time.Second))

	client, err := NewClient(clientNode, namespace, nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	err = client.Delete("delete-key", 5*time.Second, clusterNode.ID())
	require.NoError(t, err, "Delete should succeed")

	_, found := cl.Get("delete-key")
	require.False(t, found, "key should be deleted")
}

func TestClient_Keys_Success_Integration(t *testing.T) {
	clusterNode := newTestNode(t)
	clientNode := newTestNode(t)

	namespace := "test-client-keys"

	cl, err := New(clusterNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")
	linkTo(t, clientNode, clusterNode)

	require.NoError(t, cl.Set("keys/a", []byte("a"), time.Second))
	require.NoError(t, cl.Set("keys/b", []byte("b"), time.Second))
	require.NoError(t, cl.Set("keys/c", []byte("c"), time.Second))

	client, err := NewClient(clientNode, namespace, nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	keys, err := client.Keys("keys/", clusterNode.ID())
	require.NoError(t, err, "Keys should succeed")
	require.GreaterOrEqual(t, len(keys), 3, "should find at least 3 keys")
}

func TestClient_ExchangePeers_Success_Integration(t *testing.T) {
	clusterNode := newTestNode(t)
	clientNode := newTestNode(t)

	namespace := "test-client-exchange"

	cl, err := New(clusterNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")
	linkTo(t, clientNode, clusterNode)

	client, err := newInternalClient(clientNode, namespace, nil, quickRetry)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	ourStart := time.Now()
	ourPeers := map[string]int64{
		clientNode.ID().String(): 0,
	}

	theirStart, theirPeers, err := client.ExchangePeers(ourStart, ourPeers, clusterNode.ID())
	require.NoError(t, err, "ExchangePeers should succeed")
	require.False(t, theirStart.IsZero(), "theirStart should not be zero")
	require.NotNil(t, theirPeers, "theirPeers should not be nil")
}

func TestClient_JoinVoter_Success_Integration(t *testing.T) {
	clusterNode := newTestNode(t)
	clientNode := newTestNode(t)

	namespace := "test-client-join"

	cl, err := New(clusterNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")
	linkTo(t, clientNode, clusterNode)

	client, err := newInternalClient(clientNode, namespace, nil, quickRetry)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	err = client.JoinVoter(t.Context(), clientNode.ID(), 5*time.Second, formationOf(cl), 0, clusterNode.ID())
	require.NoError(t, err, "JoinVoter should succeed")
}

func TestClient_Set_WithEncryption_Success_Integration(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	clusterNode := newTestNode(t)
	clientNode := newTestNode(t)

	namespace := "test-client-enc-set"

	cl, err := New(clusterNode, namespace, WithEncryptionKey(key), WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create encrypted cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")
	linkTo(t, clientNode, clusterNode)

	block, err := aes.NewCipher(key)
	require.NoError(t, err, "failed to create cipher")
	gcm, err := cipher.NewGCMWithNonceSize(block, nonceSize)
	require.NoError(t, err, "failed to create GCM")
	client, err := NewClient(clientNode, namespace, gcm)
	require.NoError(t, err, "failed to create encrypted client")
	defer client.Close()

	err = client.Set("enc-set-key", []byte("enc-set-value"), 5*time.Second, clusterNode.ID())
	require.NoError(t, err, "Set with encryption should succeed")

	val, found := cl.Get("enc-set-key")
	require.True(t, found, "key should exist after encrypted Set")
	assert.Equal(t, string(val), "enc-set-value")
}

func TestClient_Get_WithEncryption_Success_Integration(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	clusterNode := newTestNode(t)
	clientNode := newTestNode(t)

	namespace := "test-client-enc-get"

	cl, err := New(clusterNode, namespace, WithEncryptionKey(key), WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create encrypted cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")
	linkTo(t, clientNode, clusterNode)

	require.NoError(t, cl.Set("enc-get-key", []byte("enc-get-value"), time.Second))

	block, err := aes.NewCipher(key)
	require.NoError(t, err, "failed to create cipher")
	gcm, err := cipher.NewGCMWithNonceSize(block, nonceSize)
	require.NoError(t, err, "failed to create GCM")
	client, err := NewClient(clientNode, namespace, gcm)
	require.NoError(t, err, "failed to create encrypted client")
	defer client.Close()

	val, found, err := client.Get("enc-get-key", 0, clusterNode.ID())
	require.NoError(t, err, "Get with encryption should succeed")
	require.True(t, found, "key should be found")
	assert.Equal(t, string(val), "enc-get-value")
}

func TestClient_Delete_WithEncryption_Success_Integration(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	clusterNode := newTestNode(t)
	clientNode := newTestNode(t)

	namespace := "test-client-enc-del"

	cl, err := New(clusterNode, namespace, WithEncryptionKey(key), WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create encrypted cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")
	linkTo(t, clientNode, clusterNode)

	require.NoError(t, cl.Set("enc-del-key", []byte("enc-del-value"), time.Second))

	block, err := aes.NewCipher(key)
	require.NoError(t, err, "failed to create cipher")
	gcm, err := cipher.NewGCMWithNonceSize(block, nonceSize)
	require.NoError(t, err, "failed to create GCM")
	client, err := NewClient(clientNode, namespace, gcm)
	require.NoError(t, err, "failed to create encrypted client")
	defer client.Close()

	err = client.Delete("enc-del-key", 5*time.Second, clusterNode.ID())
	require.NoError(t, err, "Delete with encryption should succeed")

	_, found := cl.Get("enc-del-key")
	require.False(t, found, "key should be deleted")
}

func TestClient_Keys_WithEncryption_Success_Integration(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	clusterNode := newTestNode(t)
	clientNode := newTestNode(t)

	namespace := "test-client-enc-keys"

	cl, err := New(clusterNode, namespace, WithEncryptionKey(key), WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create encrypted cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")
	linkTo(t, clientNode, clusterNode)

	require.NoError(t, cl.Set("enc-keys/a", []byte("a"), time.Second))
	require.NoError(t, cl.Set("enc-keys/b", []byte("b"), time.Second))

	block, err := aes.NewCipher(key)
	require.NoError(t, err, "failed to create cipher")
	gcm, err := cipher.NewGCMWithNonceSize(block, nonceSize)
	require.NoError(t, err, "failed to create GCM")
	client, err := NewClient(clientNode, namespace, gcm)
	require.NoError(t, err, "failed to create encrypted client")
	defer client.Close()

	keys, err := client.Keys("enc-keys/", clusterNode.ID())
	require.NoError(t, err, "Keys with encryption should succeed")
	require.NotNil(t, keys, "keys should not be nil")
	require.GreaterOrEqual(t, len(keys), 2, "should find at least 2 keys")
}
