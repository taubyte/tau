package app

import (
	"bytes"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	originalText := "Hello, World!"
	password := "strongpassword"

	// Encrypt the original text
	encryptedData, err := encrypt([]byte(originalText), password)
	if err != nil {
		t.Fatalf("Failed to encrypt: %s", err)
	}

	// Decrypt the encrypted data
	decryptedData, err := decrypt(encryptedData, password)
	if err != nil {
		t.Fatalf("Failed to decrypt: %s", err)
	}

	// Check if decrypted data matches the original text
	if !bytes.Equal(decryptedData, []byte(originalText)) {
		t.Errorf("Decrypted data does not match original. got: %s, want: %s", decryptedData, originalText)
	}
}

func TestDecryptWithWrongPassword(t *testing.T) {
	originalText := "Sensitive data here"
	password := "correctpassword"
	wrongPassword := "wrongpassword"

	encryptedData, err := encrypt([]byte(originalText), password)
	if err != nil {
		t.Fatalf("Failed to encrypt: %s", err)
	}

	_, err = decrypt(encryptedData, password)
	if err != nil {
		t.Errorf("decryption failed with %s", err)
	}

	_, err = decrypt(encryptedData, wrongPassword)
	if err == nil {
		t.Errorf("Expected an error when decrypting with wrong password, but decryption succeeded")
	}
}

func TestDecryptModifiedCiphertext(t *testing.T) {
	originalText := "Another piece of sensitive data"
	password := "anotherstrongpassword"

	encryptedData, err := encrypt([]byte(originalText), password)
	if err != nil {
		t.Fatalf("Failed to encrypt: %s", err)
	}

	// Modify the ciphertext to simulate corruption or tampering
	encryptedData[len(encryptedData)-1] ^= 0xff

	_, err = decrypt(encryptedData, password)
	if err == nil {
		t.Errorf("Expected an error when decrypting modified ciphertext, but decryption succeeded")
	}
}
