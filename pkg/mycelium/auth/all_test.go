package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"gotest.tools/v3/assert"
)

func generatePrivateKey(t *testing.T, passphrase string) (string, ssh.Signer) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NilError(t, err, "Failed to generate private key")

	var privateKeyPEM []byte
	if passphrase != "" {
		privateKeyPEMBlock := &pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
		}
		//lint:ignore SA1019 simplest way, it's just for test
		encryptedPEMBlock, err := x509.EncryptPEMBlock(rand.Reader, privateKeyPEMBlock.Type, privateKeyPEMBlock.Bytes, []byte(passphrase), x509.PEMCipherAES256)
		assert.NilError(t, err, "Failed to encrypt private key with passphrase")
		privateKeyPEM = pem.EncodeToMemory(encryptedPEMBlock)
	} else {
		privateKeyPEM = pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
		})
	}

	var signer ssh.Signer
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(privateKeyPEM, []byte(passphrase))
		assert.NilError(t, err, "Failed to parse private key with passphrase")
	} else {
		signer, err = ssh.ParsePrivateKey(privateKeyPEM)
		assert.NilError(t, err, "Failed to parse private key")
	}

	return string(privateKeyPEM), signer
}

func TestNewWithPassword(t *testing.T) {
	username := "testuser"
	password := "testpassword"

	auth, err := New(username, Password(password))
	assert.NilError(t, err, "Failed to create Auth")

	assert.Equal(t, username, auth.Username, "Expected username to match")

	assert.Equal(t, 1, len(auth.Auth), "Expected 1 auth method")
}

func TestNewWithKey(t *testing.T) {
	username := "testuser"
	privateKey, _ := generatePrivateKey(t, "")
	privateKeyReader := strings.NewReader(privateKey)

	auth, err := New(username, Key(privateKeyReader))
	assert.NilError(t, err, "Failed to create Auth with key")

	assert.Equal(t, 1, len(auth.Auth), "Expected 1 auth method")
}

func TestNewWithKey_ParseError(t *testing.T) {
	username := "testuser"
	invalidPrivateKey := `-----BEGIN RSA PRIVATE KEY-----
INVALID_KEY-----
-----END RSA PRIVATE KEY-----`
	privateKeyReader := strings.NewReader(invalidPrivateKey)

	_, err := New(username, Key(privateKeyReader))
	assert.ErrorContains(t, err, "parsing private key", "Expected parsing error")
}

func TestNewWithKeyAndPassphrase(t *testing.T) {
	username := "testuser"
	passphrase := "testpassphrase"
	privateKey, _ := generatePrivateKey(t, passphrase)
	privateKeyReader := strings.NewReader(privateKey)

	auth, err := New(username, KeyWithPassphrase(privateKeyReader, passphrase))
	assert.NilError(t, err, "Failed to create Auth with key and passphrase")

	assert.Equal(t, 1, len(auth.Auth), "Expected 1 auth method")
}

func TestNewWithKeyAndPassphrase_ParseError(t *testing.T) {
	username := "testuser"
	invalidPrivateKey := `-----BEGIN RSA PRIVATE KEY-----
INVALID_KEY-----
-----END RSA PRIVATE KEY-----`
	passphrase := "testpassphrase"
	privateKeyReader := strings.NewReader(invalidPrivateKey)

	_, err := New(username, KeyWithPassphrase(privateKeyReader, passphrase))
	assert.ErrorContains(t, err, "parsing private key with passphrase", "Expected parsing error with passphrase")
}

func TestKeyReadError(t *testing.T) {
	username := "testuser"
	errorReader := &errorReader{}

	_, err := New(username, Key(errorReader))
	assert.ErrorContains(t, err, "reading private key", "Expected reading error")
}

func TestKeyWithPassphraseReadError(t *testing.T) {
	username := "testuser"
	errorReader := &errorReader{}
	passphrase := "testpassphrase"

	_, err := New(username, KeyWithPassphrase(errorReader, passphrase))
	assert.ErrorContains(t, err, "reading private key", "Expected reading error")
}

type errorReader struct{}

func (e *errorReader) Read(p []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
