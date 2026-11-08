package raft

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"path"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	taupeer "github.com/taubyte/tau/p2p/peer"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
	"google.golang.org/protobuf/proto"
	"gotest.tools/v3/assert"
)

func keyToCipher(t *testing.T, key []byte) cipher.AEAD {
	block, err := aes.NewCipher(key)
	require.NoError(t, err, "failed to create cipher")
	gcm, err := cipher.NewGCMWithNonceSize(block, nonceSize)
	require.NoError(t, err, "failed to create GCM")
	return gcm
}

// sealedOn is namespace's group on node's engine, its messages sealed under gcm.
func sealedOn(t *testing.T, node taupeer.Node, namespace string, gcm cipher.AEAD) *group {
	t.Helper()
	e := engineFor(node)
	t.Cleanup(e.release)
	fsm := newKVFSM(t.Context(), node.Store(), path.Join(RaftStoragePrefix, namespace))
	g, err := e.open(namespace, groupConfig{timeouts: quickTimeouts, machine: fsm, cipher: gcm, store: node.Store()})
	require.NoError(t, err)
	t.Cleanup(func() { g.Close() })
	return g
}

// A GROUP'S MESSAGES ARE SEALED UNDER ITS KEY: members holding it replicate, and a
// member holding another reads none of it.
func TestAGroupsMessagesAreSealedUnderItsKey(t *testing.T) {
	key, other := make([]byte, AESKeySize), make([]byte, AESKeySize)
	rand.Read(key)
	rand.Read(other)
	nodes := linked(t, 3)

	leader := sealedOn(t, nodes[0], "sealed", keyToCipher(t, key))
	same := sealedOn(t, nodes[1], "sealed", keyToCipher(t, key))
	stranger := sealedOn(t, nodes[2], "sealed", keyToCipher(t, other))
	require.NoError(t, leader.Bootstrap([]peer.ID{nodes[0].ID(), nodes[1].ID()}))
	leading(t, leader, same)
	l := leader
	if same.State() == Leader {
		l = same
	}
	set(t, l, "k", "v")
	require.Eventually(t, func() bool { return held(leader, "k") == "v" && held(same, "k") == "v" }, 5*time.Second, 5*time.Millisecond)

	// The stranger is added, which the two that hold the key commit between them,
	// and it hears nothing it can open: it never applies.
	require.NoError(t, l.AddVoter(nodes[2].ID(), 5*time.Second))
	require.Never(t, func() bool { return held(stranger, "k") == "v" }, 300*time.Millisecond, 10*time.Millisecond)

	// A sealed message is never the clear one, and does not open under another key.
	sealed, err := seal(keyToCipher(t, key), []byte("plain"))
	require.NoError(t, err)
	assert.Assert(t, !bytes.Contains(sealed, []byte("plain")))
	_, err = unseal(keyToCipher(t, other), sealed)
	assert.ErrorContains(t, err, "decryption failed")
	_, err = unseal(keyToCipher(t, key), []byte("short"))
	assert.ErrorContains(t, err, "too short")
}

func TestCluster_WithEncryption(t *testing.T) {
	key := make([]byte, AESKeySize)
	rand.Read(key)

	node := newMockNode(t)

	cl, err := New(node, "/raft/encrypted-test", WithEncryptionKey(key), WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create encrypted cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = cl.WaitForLeader(ctx)
	require.NoError(t, err, "leader election should succeed with fast timeouts")

	assert.Assert(t, cl.IsLeader(), "single node should be leader")

	err = cl.Set("enc-key", []byte("enc-value"), time.Second)
	require.NoError(t, err, "Set on encrypted cluster should succeed")

	val, found := cl.Get("enc-key")
	assert.Assert(t, found, "key should be found")
	assert.Equal(t, string(val), "enc-value")

	err = cl.Delete("enc-key", time.Second)
	require.NoError(t, err, "Delete on encrypted cluster should succeed")

	_, found = cl.Get("enc-key")
	assert.Assert(t, !found, "key should be gone after delete")
}

func TestCluster_Encryption_Transport(t *testing.T) {
	key := make([]byte, AESKeySize)
	rand.Read(key)

	node := newMockNode(t)

	cl, err := New(node, "/raft/transport-enc-test", WithEncryptionKey(key), WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err, "failed to create encrypted cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = cl.WaitForLeader(ctx)
	require.NoError(t, err, "leader election should succeed with fast timeouts")
}

func TestSealedCodec_RoundTrip(t *testing.T) {
	key := make([]byte, AESKeySize)
	rand.Read(key)
	codec := sealedCodec{gcm: keyToCipher(t, key)}

	value := []byte("value")
	in := &raftv2.SetRequest{Key: "key", Value: value, TimeoutMs: 42}
	sealed1, err := codec.Marshal(in)
	require.NoError(t, err)
	sealed2, err := codec.Marshal(in)
	require.NoError(t, err)
	// A fresh nonce each time, so one message never seals the same way twice.
	assert.Assert(t, !bytes.Equal(sealed1, sealed2))

	plain, err := proto.Marshal(in)
	require.NoError(t, err)
	assert.Assert(t, !bytes.Contains(sealed1, plain), "the proto encoding is on the wire in the clear")

	out := &raftv2.SetRequest{}
	require.NoError(t, codec.Unmarshal(sealed1, out))
	assert.Assert(t, proto.Equal(in, out))
}

func TestSealedCodec_Refuses(t *testing.T) {
	key1 := make([]byte, AESKeySize)
	key2 := make([]byte, AESKeySize)
	rand.Read(key1)
	rand.Read(key2)
	codec := sealedCodec{gcm: keyToCipher(t, key1)}

	sealed, err := codec.Marshal(&raftv2.KeysRequest{Prefix: "p"})
	require.NoError(t, err)

	err = sealedCodec{gcm: keyToCipher(t, key2)}.Unmarshal(sealed, &raftv2.KeysRequest{})
	assert.ErrorContains(t, err, "decryption failed")

	plain, err := proto.Marshal(&raftv2.KeysRequest{Prefix: "a longer prefix in the clear"})
	require.NoError(t, err)
	err = codec.Unmarshal(plain, &raftv2.KeysRequest{})
	assert.ErrorContains(t, err, "decryption failed")

	err = codec.Unmarshal([]byte("short"), &raftv2.KeysRequest{})
	assert.ErrorContains(t, err, "too short")

	_, err = codec.Marshal("not a message")
	assert.ErrorContains(t, err, "not a proto message")
	err = codec.Unmarshal(sealed, new(string))
	assert.ErrorContains(t, err, "not a proto message")
}
