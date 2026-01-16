package engine

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestIsDomainName_EdgeCases(t *testing.T) {
	// Use case: Testing isDomainName with various edge cases to improve coverage

	// Valid cases
	assert.Equal(t, isDomainName("example.com"), true)
	assert.Equal(t, isDomainName("sub.example.com"), true)
	assert.Equal(t, isDomainName("a.b.c.d.example.com"), true)
	assert.Equal(t, isDomainName("example.co.uk"), true)

	// Invalid cases - these should cover more paths in the function
	assert.Equal(t, isDomainName(""), false)             // Empty string
	assert.Equal(t, isDomainName("."), true)             // Root domain is valid (see golang.org/issue/45715)
	assert.Equal(t, isDomainName("example..com"), false) // Double dot
	assert.Equal(t, isDomainName("-example.com"), false) // Starts with hyphen
	assert.Equal(t, isDomainName("example-.com"), false) // Ends with hyphen in label
	assert.Equal(t, isDomainName("example.com-"), false) // Ends with hyphen
	assert.Equal(t, isDomainName("example..com"), false) // Consecutive dots
	assert.Equal(t, isDomainName(".example.com"), false) // Starts with dot
	// Note: "example.com." is valid (254 chars with trailing dot is valid per RFC)

	// Long domain names (254+ chars)
	longDomain := "a." + string(make([]byte, 250)) + ".com"
	if len(longDomain) > 254 {
		assert.Equal(t, isDomainName(longDomain), false) // Too long
	}

	// Domain with special characters that should fail
	assert.Equal(t, isDomainName("example@com"), false)
	assert.Equal(t, isDomainName("example com"), false)  // Space
	assert.Equal(t, isDomainName("example\tcom"), false) // Tab
}
