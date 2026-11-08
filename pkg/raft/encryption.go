package raft

import (
	"crypto/cipher"
	"crypto/rand"
	"fmt"

	"github.com/taubyte/tau/pkg/proto/codec"
)

const (
	AESKeySize = 32
	nonceSize  = 12
)

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
	plain, err := codec.Codec.Marshal(m)
	if err != nil {
		return nil, err
	}
	return seal(c.gcm, plain)
}

func (c sealedCodec) Unmarshal(data []byte, m any) error {
	plain, err := unseal(c.gcm, data)
	if err != nil {
		return err
	}
	return codec.Codec.Unmarshal(plain, m)
}

// seal is plain under gcm: a random nonce, then the AEAD of plain.
func seal(gcm cipher.AEAD, plain []byte) ([]byte, error) {
	nonce := make([]byte, nonceSize, nonceSize+len(plain)+gcm.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

// unseal is what seal sealed, or why it cannot be read.
func unseal(gcm cipher.AEAD, data []byte) ([]byte, error) {
	if len(data) < nonceSize {
		return nil, fmt.Errorf("encrypted data too short: expected at least %d bytes, got %d", nonceSize, len(data))
	}
	plain, err := gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}
	return plain, nil
}
