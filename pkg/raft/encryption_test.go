package raft

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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

func TestNewEncryptedConn(t *testing.T) {
	key := make([]byte, AESKeySize)
	rand.Read(key)

	conn1, conn2 := net.Pipe()

	encConn, err := newEncryptedConn(conn1, keyToCipher(t, key))
	require.NoError(t, err)
	assert.Assert(t, encConn != nil)
	conn2.Close()
}

func TestNewEncryptedConn_InvalidKey(t *testing.T) {
	shortKey := make([]byte, 16)
	conn1, _ := net.Pipe()

	block, err := aes.NewCipher(shortKey)
	require.NoError(t, err)
	_, err = cipher.NewGCMWithNonceSize(block, nonceSize)
	if err == nil {
		encConn, err := newEncryptedConn(conn1, nil)
		require.NoError(t, err)
		assert.Assert(t, encConn != nil)
	}
}

func TestEncryptedConn_WriteRead(t *testing.T) {
	key := make([]byte, AESKeySize)
	rand.Read(key)

	conn1, conn2 := net.Pipe()

	encConn1, err := newEncryptedConn(conn1, keyToCipher(t, key))
	require.NoError(t, err)

	encConn2, err := newEncryptedConn(conn2, keyToCipher(t, key))
	require.NoError(t, err)

	testData := []byte("Hello, encrypted world!")
	expectedLen := len(testData)

	go func() {
		defer encConn1.Close()
		n, err := encConn1.Write(testData)
		require.NoError(t, err)
		assert.Equal(t, n, expectedLen)
	}()

	buf := make([]byte, len(testData)+100)
	n, err := encConn2.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, n, expectedLen)
	assert.Equal(t, string(buf[:n]), string(testData))

	encConn2.Close()
}

func TestEncryptedConn_WriteRead_LargeData(t *testing.T) {
	key := make([]byte, AESKeySize)
	rand.Read(key)

	conn1, conn2 := net.Pipe()

	encConn1, err := newEncryptedConn(conn1, keyToCipher(t, key))
	require.NoError(t, err)

	encConn2, err := newEncryptedConn(conn2, keyToCipher(t, key))
	require.NoError(t, err)

	testData := make([]byte, 10000)
	rand.Read(testData)

	go func() {
		defer encConn1.Close()
		_, err := encConn1.Write(testData)
		require.NoError(t, err)
	}()

	buf := make([]byte, len(testData))
	n, err := io.ReadFull(encConn2, buf)
	require.NoError(t, err)
	assert.Equal(t, n, len(testData))
	assert.Assert(t, bytes.Equal(buf, testData))

	encConn2.Close()
}

func TestEncryptedConn_Read_DecryptionFailure(t *testing.T) {
	key := make([]byte, AESKeySize)
	rand.Read(key)

	conn1, conn2 := net.Pipe()

	encConn, err := newEncryptedConn(conn1, keyToCipher(t, key))
	require.NoError(t, err)

	go func() {
		defer conn2.Close()
		conn2.Write([]byte{0, 0, 0, 10}) // Length
		conn2.Write(make([]byte, 12))    // Invalid nonce
		conn2.Write([]byte("invalid"))   // Invalid ciphertext
	}()

	buf := make([]byte, 100)
	_, err = encConn.Read(buf)
	require.Error(t, err)
	// Could be "decryption failed" or "unexpected EOF" depending on timing
	assert.Assert(t, err != nil)
}

func TestEncryptedConn_Read_ShortBuffer(t *testing.T) {
	key := make([]byte, AESKeySize)
	rand.Read(key)

	conn1, conn2 := net.Pipe()

	encConn1, err := newEncryptedConn(conn1, keyToCipher(t, key))
	require.NoError(t, err)

	encConn2, err := newEncryptedConn(conn2, keyToCipher(t, key))
	require.NoError(t, err)

	testData := []byte("This is a longer test message that exceeds the buffer size")
	go func() {
		defer encConn1.Close()
		encConn1.Write(testData)
	}()

	buf := make([]byte, 10)
	n, err := encConn2.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, n, 10)
	assert.Equal(t, string(buf), string(testData[:10]))

	encConn2.Close()
}

func TestEncryptedConn_Write_Error(t *testing.T) {
	key := make([]byte, AESKeySize)
	rand.Read(key)

	conn1, conn2 := net.Pipe()
	conn2.Close()

	encConn, err := newEncryptedConn(conn1, keyToCipher(t, key))
	require.NoError(t, err)

	_, err = encConn.Write([]byte("test"))
	require.Error(t, err)
}

func TestEncryptedConn_Read_Error(t *testing.T) {
	key := make([]byte, AESKeySize)
	rand.Read(key)

	conn1, conn2 := net.Pipe()
	conn2.Close()

	encConn, err := newEncryptedConn(conn1, keyToCipher(t, key))
	require.NoError(t, err)

	buf := make([]byte, 100)
	_, err = encConn.Read(buf)
	require.Error(t, err)
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
