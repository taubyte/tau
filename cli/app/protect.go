package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/term"
)

var testPassword = ""

func promptPassword(prompt string) (string, error) {
	if testPassword != "" {
		return testPassword, nil
	}
	fmt.Print(prompt)

	passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}

	fmt.Println()

	return string(passwordBytes), nil
}

// generates a cryptographic key from a password using PBKDF2.
func deriveKey(password string, salt []byte, keyLen int) []byte {
	return pbkdf2.Key([]byte(password), salt, 4096, keyLen, sha256.New)
}

// AES-256-GCM: authenticated, so a wrong password or tampered ciphertext
// fails the tag check instead of decrypting to garbage. Blob layout:
// salt(8) || nonce(12) || sealed(ciphertext+tag).
func gcm(password string, salt []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(deriveKey(password, salt, 32))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func encrypt(data []byte, password string) ([]byte, error) {
	salt := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}

	aead, err := gcm(password, salt)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	out := append(salt, nonce...)
	return aead.Seal(out, nonce, data, nil), nil
}

func decrypt(data []byte, password string) ([]byte, error) {
	if len(data) < 8 {
		return nil, errors.New("cipherData too short")
	}

	aead, err := gcm(password, data[:8])
	if err != nil {
		return nil, err
	}

	if len(data) < 8+aead.NonceSize() {
		return nil, errors.New("cipherData too short")
	}
	nonce := data[8 : 8+aead.NonceSize()]

	return aead.Open(nil, nonce, data[8+aead.NonceSize():], nil)
}
