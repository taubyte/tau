package raft

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
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

func (ec *encryptedConn) Read(b []byte) (int, error) {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(ec.Conn, lenBuf); err != nil {
		return 0, err
	}
	payloadLen := binary.BigEndian.Uint32(lenBuf)

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(ec.Conn, nonce); err != nil {
		return 0, err
	}

	ciphertext := make([]byte, payloadLen)
	if _, err := io.ReadFull(ec.Conn, ciphertext); err != nil {
		return 0, err
	}

	plaintext, err := ec.decrypt.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return 0, fmt.Errorf("decryption failed: %w", err)
	}

	n := copy(b, plaintext)
	if n < len(plaintext) {
		return n, nil
	}

	return n, nil
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

// encryptBody encrypts a command body or response for stream service
func encryptBody(body command.Body, gcm cipher.AEAD) (command.Body, error) {
	if gcm == nil {
		return nil, fmt.Errorf("encryption cipher is required")
	}

	bodyBytes, err := cbor.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("serializing body to CBOR: %w", err)
	}

	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, bodyBytes, nil)
	encrypted := append(nonce, ciphertext...)

	return command.Body{
		"data": encrypted,
	}, nil
}

// decryptBody decrypts a command body or response from stream service
func decryptBody(body command.Body, gcm cipher.AEAD) (command.Body, error) {
	if gcm == nil {
		return nil, fmt.Errorf("decryption cipher is required")
	}

	encryptedData, ok := body["data"]
	if !ok {
		return nil, fmt.Errorf("body is not encrypted but decryption cipher is required")
	}

	encrypted, ok := encryptedData.([]byte)
	if !ok {
		return nil, fmt.Errorf("invalid encrypted data type: expected []byte, got %T", encryptedData)
	}

	if len(encrypted) < nonceSize {
		return nil, fmt.Errorf("encrypted data too short: expected at least %d bytes, got %d", nonceSize, len(encrypted))
	}

	nonce := encrypted[:nonceSize]
	ciphertext := encrypted[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	var decryptedBody command.Body
	if err := cbor.Unmarshal(plaintext, &decryptedBody); err != nil {
		return nil, fmt.Errorf("deserializing body from CBOR: %w", err)
	}

	return decryptedBody, nil
}

// encryptResponse encrypts a response for stream service
func encryptResponse(resp cr.Response, gcm cipher.AEAD) (cr.Response, error) {
	if gcm == nil {
		return nil, fmt.Errorf("encryption cipher is required")
	}

	respBytes, err := cbor.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("serializing response to CBOR: %w", err)
	}

	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, respBytes, nil)
	encrypted := append(nonce, ciphertext...)

	return cr.Response{
		"data": encrypted,
	}, nil
}

// decryptResponse decrypts a response from stream service
func decryptResponse(resp cr.Response, gcm cipher.AEAD) (cr.Response, error) {
	if gcm == nil {
		return nil, fmt.Errorf("decryption cipher is required")
	}

	encryptedData, ok := resp["data"]
	if !ok {
		return nil, fmt.Errorf("response is not encrypted but decryption cipher is required")
	}

	encrypted, ok := encryptedData.([]byte)
	if !ok {
		return nil, fmt.Errorf("invalid encrypted data type: expected []byte, got %T", encryptedData)
	}

	if len(encrypted) < nonceSize {
		return nil, fmt.Errorf("encrypted data too short: expected at least %d bytes, got %d", nonceSize, len(encrypted))
	}

	nonce := encrypted[:nonceSize]
	ciphertext := encrypted[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	var decryptedResp cr.Response
	if err := cbor.Unmarshal(plaintext, &decryptedResp); err != nil {
		return nil, fmt.Errorf("deserializing response from CBOR: %w", err)
	}

	return decryptedResp, nil
}
