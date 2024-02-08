package app

import (
	"bytes"
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

func encrypt(data []byte, password string) ([]byte, error) {
	salt := make([]byte, 8)
	_, err := io.ReadFull(rand.Reader, salt)
	if err != nil {
		return nil, err
	}
	key := deriveKey(password, salt, 32) // AES-256

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	blockSize := block.BlockSize()
	data = _PKCS7Pad(data, blockSize)
	cipherText := make([]byte, blockSize+len(data))
	iv := cipherText[:blockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}

	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(cipherText[blockSize:], data)

	return append(salt, cipherText...), nil
}

func decrypt(data []byte, password string) ([]byte, error) {
	salt := data[:8]
	data = data[8:]

	key := deriveKey(password, salt, 32) // AES-256

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	if len(data) < block.BlockSize() {
		return nil, errors.New("cipherData too short")
	}

	if len(data)%block.BlockSize() != 0 {
		return nil, errors.New("cipherData malformed")
	}

	iv := data[:block.BlockSize()]
	data = data[block.BlockSize():]

	mode := cipher.NewCBCDecrypter(block, iv)
	var bdata []byte = make([]byte, len(data))
	mode.CryptBlocks(bdata, data)
	bdata, err = _PKCS7Unpad(bdata, block.BlockSize())
	if err != nil {
		return nil, err
	}

	return bdata, nil
}

func _PKCS7Pad(message []byte, blockSize int) []byte {
	padding := blockSize - len(message)%blockSize
	padText := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(message, padText...)
}

func _PKCS7Unpad(message []byte, blockSize int) ([]byte, error) {
	length := len(message)
	if length == 0 || length%blockSize != 0 {
		return nil, errors.New("invalid padding size")
	}

	padLength := int(message[length-1])
	if padLength > blockSize || padLength == 0 {
		return nil, errors.New("invalid padding")
	}

	for _, val := range message[length-padLength:] {
		if int(val) != padLength {
			return nil, errors.New("invalid padding")
		}
	}

	return message[:(length - padLength)], nil
}
