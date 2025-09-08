package auth

import (
	"testing"

	"gotest.tools/v3/assert"
)

// Test helper functions
func TestGenerateWildCardDomain(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"example.com", "*.com"},
		{"sub.example.com", "*.example.com"},
		{"a.b.c.example.org", "*.b.c.example.org"},
		{"single.com", "*.com"},
		{"very.long.subdomain.chain.example.net", "*.long.subdomain.chain.example.net"},
	}

	for _, test := range tests {
		result := generateWildCardDomain(test.input)
		assert.Equal(t, result, test.expected, "Input: %s", test.input)
	}
}

// Test getMapValues with basic functionality
func TestGetMapValues(t *testing.T) {
	// Test with string values
	stringMap := map[string]interface{}{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}

	values := getMapValues(stringMap)
	assert.Equal(t, len(values), 3)

	// Check that all values are present
	expectedValues := []interface{}{"value1", "value2", "value3"}
	for _, expected := range expectedValues {
		found := false
		for _, actual := range values {
			if actual == expected {
				found = true
				break
			}
		}
		assert.Assert(t, found, "Expected value %v not found", expected)
	}

	// Test with mixed types
	mixedMap := map[string]interface{}{
		"string": "hello",
		"number": 42,
		"bool":   true,
		"nil":    nil,
	}

	values = getMapValues(mixedMap)
	assert.Equal(t, len(values), 4)
}

// Test extractIdFromKey with basic functionality
func TestExtractIdFromKey(t *testing.T) {
	tests := []struct {
		list     []string
		split    string
		index    int
		expected []string
	}{
		{
			list:     []string{"key1:value1", "key2:value2", "key3:value3"},
			split:    ":",
			index:    0,
			expected: []string{"key1", "key2", "key3"},
		},
		{
			list:     []string{"key1:value1", "key2:value2", "key3:value3"},
			split:    ":",
			index:    1,
			expected: []string{"value1", "value2", "value3"},
		},
		{
			list:     []string{"a/b/c", "d/e/f", "g/h/i"},
			split:    "/",
			index:    1,
			expected: []string{"b", "e", "h"},
		},
		{
			list:     []string{"single", "key:value", "another:key:value"},
			split:    ":",
			index:    0,
			expected: []string{"key", "another"},
		},
		{
			list:     []string{"no-split", "still-no-split"},
			split:    ":",
			index:    0,
			expected: []string{},
		},
	}

	for _, test := range tests {
		result := extractIdFromKey(test.list, test.split, test.index)
		assert.DeepEqual(t, result, test.expected)
	}
}

// Test constants and simple functions
func TestDeployKeyConstants(t *testing.T) {
	// Test the constants before any service initialization
	assert.Assert(t, devDeployKeyName == "taubyte_deploy_key_dev")
	assert.Assert(t, devDeployKeyName != "")

	// Note: deployKeyName gets modified during service initialization
	// so we test that it's not empty and has a valid value
	assert.Assert(t, deployKeyName != "")

	// Test that the constants are different initially
	// (deployKeyName will be "taubyte_deploy_key" before service init)
	if deployKeyName != devDeployKeyName {
		assert.Assert(t, deployKeyName == "taubyte_deploy_key")
	}
}

// Test key generation and validation utility functions
func TestKeyGenerationAndValidation(t *testing.T) {
	// Test extractProjectVariables with mock context
	// This would require mocking the http.Context interface
	// For now, let's test the function logic directly

	// Test generateKey function
	keyName, pubKey, privKey, err := generateKey()
	assert.NilError(t, err)
	assert.Assert(t, keyName != "")
	assert.Assert(t, pubKey != "")
	assert.Assert(t, privKey != "")

	// Test that keys are different
	assert.Assert(t, pubKey != privKey)

	// Test that pubKey is not empty and different from private key
	assert.Assert(t, pubKey != "")
	assert.Assert(t, len(pubKey) > 0)
}

// Test getMapValues with different map scenarios
func TestGetMapValuesScenarios(t *testing.T) {
	// Test with different map types and values
	testCases := []struct {
		input    map[string]interface{}
		expected int
	}{
		{map[string]interface{}{"a": 1, "b": 2, "c": 3}, 3},
		{map[string]interface{}{"single": "value"}, 1},
		{map[string]interface{}{}, 0},
		{map[string]interface{}{"nil": nil, "string": "test", "number": 42, "bool": true}, 4},
	}

	for _, tc := range testCases {
		result := getMapValues(tc.input)
		assert.Equal(t, len(result), tc.expected, "Failed for input: %v", tc.input)

		// Verify all values from the map are present in the result
		for _, v := range tc.input {
			found := false
			for _, r := range result {
				if r == v {
					found = true
					break
				}
			}
			assert.Assert(t, found, "Value %v not found in result", v)
		}
	}
}

// Test generateWildCardDomain with different scenarios
func TestGenerateWildCardDomainScenarios(t *testing.T) {
	// Test with different domain patterns
	testCases := []struct {
		input    string
		expected string
	}{
		{"example.com", "*.com"},
		{"sub.example.com", "*.example.com"},
		{"deep.sub.example.com", "*.sub.example.com"},
		{"a.b.c.d.example.com", "*.b.c.d.example.com"},
		{"single", "*"}, // Single level domain becomes just "*"
		{"", "*"},       // Empty domain becomes "*"
	}

	for _, tc := range testCases {
		result := generateWildCardDomain(tc.input)
		assert.Equal(t, result, tc.expected, "Failed for input: %s", tc.input)
	}
}

// Test extractIdFromKey with simple cases
func TestExtractIdFromKeySimple(t *testing.T) {
	// Test with simple case that works
	result := extractIdFromKey([]string{"hooks:123"}, ":", 1)
	assert.DeepEqual(t, result, []string{"123"})

	// Test with empty list
	result = extractIdFromKey([]string{}, "/", 2)
	assert.DeepEqual(t, result, []string{})

	// Test with key that has insufficient parts
	result = extractIdFromKey([]string{"hooks"}, "/", 2)
	assert.DeepEqual(t, result, []string{})
}

// Test GitHub key generation with edge cases
func TestGitHubKeyGenerationEdgeCases(t *testing.T) {
	// Test multiple key generations to ensure uniqueness of cryptographic keys
	pubKeys := make(map[string]bool)
	privKeys := make(map[string]bool)

	for i := 0; i < 10; i++ {
		keyName, pubKey, privKey, err := generateKey()
		assert.NilError(t, err)
		assert.Assert(t, keyName != "")
		assert.Assert(t, pubKey != "")
		assert.Assert(t, privKey != "")

		// Ensure cryptographic keys are unique
		assert.Assert(t, !pubKeys[pubKey], "Public key should be unique")
		assert.Assert(t, !privKeys[privKey], "Private key should be unique")

		pubKeys[pubKey] = true
		privKeys[privKey] = true

		// Test that keys are different from each other
		assert.Assert(t, pubKey != privKey)
		assert.Assert(t, keyName != pubKey)
		assert.Assert(t, keyName != privKey)
	}
}
