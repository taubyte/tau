package raft

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/require"
	taupeer "github.com/taubyte/tau/p2p/peer"
)

func TestNewClient(t *testing.T) {
	node := newMockNode(t)

	client, err := NewClient(node, "/raft/test", nil)
	require.NoError(t, err, "failed to create client")
	require.NotNil(t, client, "client should not be nil")
	defer client.Close()
}

// TestSentinel_RestoresWhatCrossedTheWireAsText: a remote error arrives as its
// text alone, and a write forwarded to a leader that lost its lease mid-commit
// could not be told from a failure that applied nothing.
func TestSentinel_RestoresWhatCrossedTheWireAsText(t *testing.T) {
	for _, sent := range []error{ErrNoLeader, ErrInvalidBarrier, ErrNotLeader, raft.ErrLeadershipLost} {
		received := connect.NewError(connect.CodeUnknown, errors.New(sent.Error()))
		require.ErrorIs(t, sentinel(received), sent)
	}
}

func TestClient_Close(t *testing.T) {
	node := newMockNode(t)

	client, err := NewClient(node, "/raft/test", nil)
	require.NoError(t, err, "failed to create client")
	require.NotNil(t, client, "client should not be nil")

	err = client.Close()
	require.NoError(t, err, "close should succeed")
}

// TestClient_SetGetDelete_Integration: what a client on another node writes
// reads back and is listed, and once it deletes a key the key is gone.
func TestClient_SetGetDelete_Integration(t *testing.T) {
	cl, cli := callerOf(t, "client-round-trip", sameStore)
	member := idOf(cl)

	require.NoError(t, cli.Set("client/a", []byte("a"), time.Second, member))
	require.NoError(t, cli.Set("client/b", []byte("b"), time.Second, member))

	value, found, err := cli.Get("client/a", 0, member)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("a"), value)
	keys, err := cli.Keys("client/", member)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"client/a", "client/b"}, keys)

	require.NoError(t, cli.Delete("client/a", time.Second, member))
	value, found, err = cli.Get("client/a", 0, member)
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, value)
	keys, err = cli.Keys("client/", member)
	require.NoError(t, err)
	require.Equal(t, []string{"client/b"}, keys)
}

// TestClient_ExchangePeers: the member comes away from an exchange knowing the
// peers its caller had seen, and the caller with the member's.
func TestClient_ExchangePeers(t *testing.T) {
	cl, cli := callerOf(t, "client-exchange-seen", sameStore)
	seen := newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())

	_, theirPeers, err := cli.ExchangePeers(time.Now(), map[string]int64{
		cli.node.ID().String(): 0,
		seen.ID().String():     100,
	}, idOf(cl))
	require.NoError(t, err)
	require.Contains(t, theirPeers, idOf(cl).String())
	require.Contains(t, theirPeers, seen.ID().String(), "the member did not take the peer its caller had seen")
	require.Contains(t, cl.(*cluster).tracker.allPeers(), seen.ID())
}

// TestClient_WithEncryption: a client holding the group's key reads and writes
// it from another node, and one without the key is answered with nothing.
func TestClient_WithEncryption(t *testing.T) {
	const namespace = "client-encrypted"
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	member := newMockNode(t)
	caller := newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())

	cl, err := New(member, namespace, WithEncryptionKey(key),
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	waitLeader(t, cl, 5*time.Second)
	connected(t, caller, cl)

	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCMWithNonceSize(block, nonceSize)
	require.NoError(t, err)
	cli, err := NewClient(caller, namespace, gcm)
	require.NoError(t, err)
	defer cli.Close()

	require.NoError(t, cli.Set("enc-key", []byte("enc-value"), time.Second, member.ID()))
	value, found, err := cli.Get("enc-key", 0, member.ID())
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("enc-value"), value)
	keys, err := cli.Keys("enc-", member.ID())
	require.NoError(t, err)
	require.Equal(t, []string{"enc-key"}, keys)
	require.NoError(t, cli.Delete("enc-key", time.Second, member.ID()))
	_, found, err = cli.Get("enc-key", 0, member.ID())
	require.NoError(t, err)
	require.False(t, found)

	plain, err := NewClient(caller, namespace, nil)
	require.NoError(t, err)
	defer plain.Close()
	require.NoError(t, cl.Set("sealed", []byte("v"), time.Second))
	_, _, err = plain.Get("sealed", 0, member.ID())
	require.Error(t, err, "a client without the key read the group")
}
