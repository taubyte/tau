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

		// Randomness is checked over many draws rather than by comparing one
		// pair. A pair says nothing at small sizes — one byte repeats once
		// every 256 draws — and sampling is correct at every size, so the
		// short case needs no exception.
		assert.Assert(t, distinctDraws(t, length, func() (string, error) {
			b, err := GenerateRandomBytes(length)
			return string(b), err
		}) > 1, "every draw of %d byte(s) returned the same value", length)
	}
}

func TestGenerateRandomString(t *testing.T) {
	// Test different lengths
	lengths := []int{1, 8, 16, 32, 64}

	for _, length := range lengths {
		str, err := GenerateRandomString(length)
		assert.NilError(t, err)
		assert.Equal(t, len(str), length)

		// See TestGenerateRandomBytes: one character out of 62 repeats often
		// enough that a single comparison is a coin flip, not a check.
		assert.Assert(t, distinctDraws(t, length, func() (string, error) {
			return GenerateRandomString(length)
		}) > 1, "every draw of %d character(s) returned the same value", length)

		// Verify all characters are valid
		for _, char := range str {
			assert.Assert(t, isValidRandomChar(char))
		}
	}
}

// distinctDraws counts how many distinct values draw produces over enough
// samples that a working generator cannot return one value by chance: the
// smallest space here is 62 symbols, and 256 draws make an all-identical run
// impossible in practice while a stuck generator fails every time.
func distinctDraws(t *testing.T, length int, draw func() (string, error)) int {
	t.Helper()

	seen := make(map[string]struct{})
	for range 256 {
		v, err := draw()
		assert.NilError(t, err)
		assert.Equal(t, len(v), length)
		seen[v] = struct{}{}
	}
	return len(seen)
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

func isValidRandomChar(char rune) bool {
	const letters = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	for _, validChar := range letters {
		if char == validChar {
			return true
		}
	}
	return false
}
