package raft

import (
	"bytes"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
)

// TestEncryptedConn_AFrameReadInPiecesArrivesWhole: raft reads its transport
// through a buffered reader, whose buffer is only partly free once it holds
// unread bytes. A frame read into a smaller buffer is handed out over as many
// reads as it takes, and the frames after it follow in order.
func TestEncryptedConn_AFrameReadInPiecesArrivesWhole(t *testing.T) {
	key := make([]byte, AESKeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)

	near, far := net.Pipe()
	defer near.Close()
	defer far.Close()
	writer, err := newEncryptedConn(near, keyToCipher(t, key))
	require.NoError(t, err)
	reader, err := newEncryptedConn(far, keyToCipher(t, key))
	require.NoError(t, err)

	frames := [][]byte{bytes.Repeat([]byte("first frame "), 20), []byte("second")}
	go func() {
		for _, frame := range frames {
			if _, err := writer.Write(frame); err != nil {
				return
			}
		}
	}()

	require.NoError(t, reader.SetReadDeadline(time.Now().Add(5*time.Second)))
	want := bytes.Join(frames, nil)
	got := make([]byte, 0, len(want))
	buf := make([]byte, 7)
	for len(got) < len(want) {
		n, err := reader.Read(buf)
		require.NoError(t, err, "after %d of %d bytes", len(got), len(want))
		got = append(got, buf[:n]...)
	}
	require.Equal(t, want, got)
}

// TestEncryptedConn_AFrameCutShortIsAnError: a peer that goes away partway
// through a frame leaves a read that fails, not one that returns what came.
func TestEncryptedConn_AFrameCutShortIsAnError(t *testing.T) {
	key := make([]byte, AESKeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		sent []byte
	}{
		{"inside the nonce", []byte{0, 0, 0, 32, 1, 2, 3}},
		{"inside the sealed payload", append([]byte{0, 0, 0, 32}, make([]byte, nonceSize+5)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			near, far := net.Pipe()
			defer far.Close()
			reader, err := newEncryptedConn(far, keyToCipher(t, key))
			require.NoError(t, err)
			go func() {
				near.Write(tc.sent)
				near.Close()
			}()

			n, err := reader.Read(make([]byte, 64))
			require.Error(t, err)
			require.Zero(t, n)
		})
	}
}

// TestEncryptedConn_AWriteToAPeerThatLeftFails: a frame goes out as its
// length, its nonce and its sealed payload, and a write that cannot finish
// any of them fails.
func TestEncryptedConn_AWriteToAPeerThatLeftFails(t *testing.T) {
	key := make([]byte, AESKeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)

	for _, tc := range []struct {
		name    string
		readsOf int
	}{
		{"before the nonce", 4},
		{"before the payload", 4 + nonceSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			near, far := net.Pipe()
			defer near.Close()
			writer, err := newEncryptedConn(near, keyToCipher(t, key))
			require.NoError(t, err)
			go func() {
				buf := make([]byte, tc.readsOf)
				read := 0
				for read < tc.readsOf {
					n, err := far.Read(buf[read:])
					if err != nil {
						break
					}
					read += n
				}
				far.Close()
			}()

			n, err := writer.Write([]byte("payload"))
			require.Error(t, err)
			require.Zero(t, n)
		})
	}
}

// TestEncryptedConn_AFrameSealedUnderAnotherKeyIsRefused: a peer holding
// another key gets nothing it sends read as plaintext.
func TestEncryptedConn_AFrameSealedUnderAnotherKeyIsRefused(t *testing.T) {
	ours, theirs := make([]byte, AESKeySize), make([]byte, AESKeySize)
	_, err := rand.Read(ours)
	require.NoError(t, err)
	_, err = rand.Read(theirs)
	require.NoError(t, err)

	near, far := net.Pipe()
	defer near.Close()
	defer far.Close()
	writer, err := newEncryptedConn(near, keyToCipher(t, theirs))
	require.NoError(t, err)
	reader, err := newEncryptedConn(far, keyToCipher(t, ours))
	require.NoError(t, err)
	go func() {
		writer.Write([]byte("not for this group"))
	}()

	n, err := reader.Read(make([]byte, 64))
	require.ErrorContains(t, err, "decryption failed")
	require.Zero(t, n)
}

// TestSealedCodec_RefusesWhatProtoCannotEncode: a message proto refuses, such
// as a string that is not UTF-8, is refused before anything is sealed.
func TestSealedCodec_RefusesWhatProtoCannotEncode(t *testing.T) {
	key := make([]byte, AESKeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)

	sealed, err := sealedCodec{gcm: keyToCipher(t, key)}.Marshal(&raftv2.KeysRequest{Prefix: "\xff"})
	require.Error(t, err)
	require.Nil(t, sealed)
}
