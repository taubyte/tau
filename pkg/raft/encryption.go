package raft

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"google.golang.org/protobuf/proto"
)

const (
	// AESKeySize is the size of the AES-256 key in bytes
	AESKeySize = 32
	// nonceSize is the size of the nonce for AES-GCM
	nonceSize = 12
)

type encryptedConn struct {
	net.Conn
	encrypt cipher.AEAD
	decrypt cipher.AEAD

	// unread is what is left of the last frame after a read whose buffer
	// could not hold all of it.
	unread []byte
}

func newEncryptedConn(conn net.Conn, gcm cipher.AEAD) (net.Conn, error) {
	if gcm == nil {
		return conn, nil
	}

	return &encryptedConn{
		Conn:    conn,
		encrypt: gcm,
		decrypt: gcm,
	}, nil
}

// Read hands a frame out over as many reads as the caller's buffer needs.
func (ec *encryptedConn) Read(b []byte) (int, error) {
	if len(ec.unread) == 0 {
		plaintext, err := ec.readFrame()
		if err != nil {
			return 0, err
		}
		ec.unread = plaintext
	}
	n := copy(b, ec.unread)
	ec.unread = ec.unread[n:]
	return n, nil
}

func (ec *encryptedConn) readFrame() ([]byte, error) {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(ec.Conn, lenBuf); err != nil {
		return nil, err
	}
	payloadLen := binary.BigEndian.Uint32(lenBuf)

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(ec.Conn, nonce); err != nil {
		return nil, err
	}

	ciphertext := make([]byte, payloadLen)
	if _, err := io.ReadFull(ec.Conn, ciphertext); err != nil {
		return nil, err
	}

	plaintext, err := ec.decrypt.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}
	return plaintext, nil
}

func (ec *encryptedConn) Write(b []byte) (int, error) {
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return 0, fmt.Errorf("generating nonce: %w", err)
	}

	ciphertext := ec.encrypt.Seal(nil, nonce, b, nil)

	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(ciphertext)))
	if _, err := ec.Conn.Write(lenBuf); err != nil {
		return 0, err
	}

	if _, err := ec.Conn.Write(nonce); err != nil {
		return 0, err
	}

	if _, err := ec.Conn.Write(ciphertext); err != nil {
		return 0, err
	}

	return len(b), nil
}

// sealedCodec is the proto codec with every message sealed under the group's
// key: a random nonce, then the AEAD of the proto encoding. It keeps the name
// "proto" so it replaces connect's own codec on both ends rather than sitting
// beside it, and a peer without the key fails to decode instead of falling
// back to the clear.
type sealedCodec struct {
	gcm cipher.AEAD
}

func (sealedCodec) Name() string { return "proto" }

func (c sealedCodec) Marshal(m any) ([]byte, error) {
	msg, ok := m.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("%T is not a proto message", m)
	}
	plain, err := proto.Marshal(msg)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize, nonceSize+len(plain)+c.gcm.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	return c.gcm.Seal(nonce, nonce, plain, nil), nil
}

func (c sealedCodec) Unmarshal(data []byte, m any) error {
	msg, ok := m.(proto.Message)
	if !ok {
		return fmt.Errorf("%T is not a proto message", m)
	}
	if len(data) < nonceSize {
		return fmt.Errorf("encrypted data too short: expected at least %d bytes, got %d", nonceSize, len(data))
	}
	plain, err := c.gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}
	return proto.Unmarshal(plain, msg)
}
