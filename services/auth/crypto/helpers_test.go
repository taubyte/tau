package crypto

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestNewUUID(t *testing.T) {
	// Test multiple UUIDs to ensure they're unique
	uuid1, err := NewUUID()
	assert.NilError(t, err)
	assert.Assert(t, uuid1 != "")
	assert.Equal(t, len(uuid1), 36) // UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx

	uuid2, err := NewUUID()
	assert.NilError(t, err)
	assert.Assert(t, uuid2 != "")
	assert.Equal(t, len(uuid2), 36)

	// UUIDs should be different
	assert.Assert(t, uuid1 != uuid2)

	// Verify UUID format (8-4-4-4-12 characters)
	assert.Assert(t, uuid1[8] == '-')
	assert.Assert(t, uuid1[13] == '-')
	assert.Assert(t, uuid1[18] == '-')
	assert.Assert(t, uuid1[23] == '-')
}

func TestGenerateRandomBytes(t *testing.T) {
	// Test different lengths
	lengths := []int{1, 16, 32, 64, 128}

	for _, length := range lengths {
		bytes, err := GenerateRandomBytes(length)
		assert.NilError(t, err)
		assert.Equal(t, len(bytes), length)

		// Generate another set to ensure randomness
		bytes2, err := GenerateRandomBytes(length)
		assert.NilError(t, err)
		assert.Equal(t, len(bytes2), length)

		// The bytes should be different (very unlikely to be the same)
		assert.Assert(t, !bytesEqual(bytes, bytes2))
	}
}

func TestGenerateRandomString(t *testing.T) {
	// Test different lengths
	lengths := []int{1, 8, 16, 32, 64}

	for _, length := range lengths {
		str, err := GenerateRandomString(length)
		assert.NilError(t, err)
		assert.Equal(t, len(str), length)

		// Generate another string to ensure randomness
		str2, err := GenerateRandomString(length)
		assert.NilError(t, err)
		assert.Equal(t, len(str2), length)

		// The strings should be different (very unlikely to be the same)
		assert.Assert(t, str != str2)

		// Verify all characters are valid
		for _, char := range str {
			assert.Assert(t, isValidRandomChar(char))
		}
	}
}

func TestGenerateSecretString(t *testing.T) {
	secret, err := GenerateSecretString()
	assert.NilError(t, err)
	assert.Equal(t, len(secret), SecretStringLength)

	// Generate another secret to ensure randomness
	secret2, err := GenerateSecretString()
	assert.NilError(t, err)
	assert.Equal(t, len(secret2), SecretStringLength)

	// The secrets should be different
	assert.Assert(t, secret != secret2)

	// Verify all characters are valid
	for _, char := range secret {
		assert.Assert(t, isValidRandomChar(char))
	}
}

func TestSecretStringLength(t *testing.T) {
	// Verify the constant is set correctly
	assert.Equal(t, SecretStringLength, 32)
}

// Helper functions for testing
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isValidRandomChar(char rune) bool {
	const letters = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	for _, validChar := range letters {
		if char == validChar {
			return true
		}
	}
	return false
}
