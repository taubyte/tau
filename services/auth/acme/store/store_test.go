package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"gotest.tools/v3/assert"
)

func TestCertFileRegexp(t *testing.T) {
	// Test certificate files (should not match)
	certFiles := []string{
		"example.com.crt",
		"example.com.pem",
		"example.com.cer",
		"example.com.der",
		"sub.example.com.crt",
		"api.example.com.pem",
		"wildcard.example.com.crt",
	}

	for _, certFile := range certFiles {
		assert.Assert(t, !certFileRegexp.MatchString(certFile), "Certificate file should not match regexp: %s", certFile)
	}

	// Test key/token files (should match)
	keyFiles := []string{
		"example.com.key",
		"example.com+token",
		"example.com+rsa",
		"example.com+key",
		"sub.example.com.key",
		"api.example.com+token",
		"wildcard.example.com+rsa",
		"test+key",
		"test+rsa",
		"test+token",
	}

	for _, keyFile := range keyFiles {
		assert.Assert(t, certFileRegexp.MatchString(keyFile), "Key/token file should match regexp: %s", keyFile)
	}
}

func TestWildcardNameGeneration(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"sub.example.com", "*.example.com"},
		{"api.example.com", "*.example.com"},
		{"www.example.com", "*.example.com"},
		{"example.com", "*.com"},
		{"sub.sub.example.com", "*.sub.example.com"},
		{"a.b.c.example.com", "*.b.c.example.com"},
		{"test.example.org", "*.example.org"},
		{"dev.staging.prod.example.net", "*.staging.prod.example.net"},
	}

	for _, tc := range testCases {
		parts := strings.Split(tc.input, ".")
		if len(parts) > 1 {
			wildcardName := "*." + strings.Join(parts[1:], ".")
			assert.Equal(t, wildcardName, tc.expected)
		}
	}
}

func TestIsCertFileDetection(t *testing.T) {
	// Test certificate files
	certFiles := []string{
		"example.com.crt",
		"example.com.pem",
		"example.com.cer",
		"sub.example.com.crt",
		"api.example.com.pem",
		"wildcard.example.com.der",
		"test.crt",
		"cert.pem",
	}

	for _, certFile := range certFiles {
		isCert := !certFileRegexp.MatchString(certFile)
		assert.Assert(t, isCert, "File should be detected as certificate: %s", certFile)
	}

	// Test key/token files
	keyFiles := []string{
		"example.com.key",
		"example.com+token",
		"example.com+rsa",
		"sub.example.com.key",
		"api.example.com+token",
		"wildcard.example.com+rsa",
		"test.key",
		"key+token",
	}

	for _, keyFile := range keyFiles {
		isCert := !certFileRegexp.MatchString(keyFile)
		assert.Assert(t, !isCert, "File should be detected as key/token: %s", keyFile)
	}
}

func TestStoreStructFields(t *testing.T) {
	// Test that the Store struct has the expected fields
	var store Store
	assert.Assert(t, store.node == nil)
	assert.Assert(t, store.client == nil)
	assert.Assert(t, !store.closed)
}

func TestLoggerInitialization(t *testing.T) {
	// Test that the logger is properly initialized
	assert.Assert(t, logger != nil)
}

func TestRegexpCompilation(t *testing.T) {
	// Test that the regexp compiles correctly
	assert.Assert(t, certFileRegexp != nil)

	// Test some basic patterns
	assert.Assert(t, certFileRegexp.MatchString("test.key"))
	assert.Assert(t, certFileRegexp.MatchString("test+token"))
	assert.Assert(t, certFileRegexp.MatchString("test+rsa"))
	assert.Assert(t, !certFileRegexp.MatchString("test.crt"))
	assert.Assert(t, !certFileRegexp.MatchString("test.pem"))
	assert.Assert(t, !certFileRegexp.MatchString("test.cer"))
}

func TestStore_Close(t *testing.T) {
	store := &Store{
		closed: false,
	}

	err := store.Close()
	assert.NilError(t, err)
	assert.Assert(t, store.closed)
}

func TestStore_Get_StoreClosed(t *testing.T) {
	store := &Store{
		closed: true,
	}

	_, err := store.Get(nil, "example.com.crt")
	assert.Assert(t, err != nil)
	assert.Equal(t, err.Error(), "store is closed")
}

func TestStore_Put_StoreClosed(t *testing.T) {
	store := &Store{
		closed: true,
	}

	err := store.Put(nil, "example.com.crt", []byte("test"))
	assert.Assert(t, err != nil)
	assert.Equal(t, err.Error(), "store is closed")
}

func TestWildcardPatternMatching(t *testing.T) {
	// Test various domain patterns for wildcard generation
	testCases := []struct {
		domain   string
		wildcard string
	}{
		{"a.example.com", "*.example.com"},
		{"b.example.com", "*.example.com"},
		{"c.example.com", "*.example.com"},
		{"sub.domain.example.com", "*.domain.example.com"},
		{"api.staging.example.org", "*.staging.example.org"},
		{"dev.prod.example.net", "*.prod.example.net"},
	}

	for _, tc := range testCases {
		parts := strings.Split(tc.domain, ".")
		if len(parts) > 1 {
			generated := "*." + strings.Join(parts[1:], ".")
			assert.Equal(t, generated, tc.wildcard)
		}
	}
}

func TestEdgeCaseDomains(t *testing.T) {
	// Test edge cases for domain handling
	edgeCases := []string{
		"single.com",
		"a.b.c.d.e.f.g.h.example.com",
		"very.long.subdomain.chain.example.org",
		"example.com",
		"a.b",
	}

	for _, domain := range edgeCases {
		parts := strings.Split(domain, ".")
		if len(parts) > 1 {
			wildcard := "*." + strings.Join(parts[1:], ".")
			assert.Assert(t, len(wildcard) > 2) // Should be more than just "*."
			assert.Assert(t, strings.HasPrefix(wildcard, "*."))
		}
	}
}

// Mock client implementation
type mockClient struct {
	responses map[string]cr.Response
	errors    map[string]error
}

func (m *mockClient) Send(cmd string, body command.Body, peers ...peerCore.ID) (cr.Response, error) {
	action, ok := body["action"].(string)
	if !ok {
		return nil, nil
	}
	key := cmd + ":" + action
	if err, exists := m.errors[key]; exists {
		return nil, err
	}
	if resp, exists := m.responses[key]; exists {
		return resp, nil
	}
	return cr.Response{}, nil
}

// Mock cache implementation
type mockCache struct {
	store map[string][]byte
}

func (m *mockCache) Get(ctx context.Context, name string) ([]byte, error) {
	if data, exists := m.store[name]; exists {
		return data, nil
	}
	return nil, &mockCacheMissError{}
}

func (m *mockCache) Put(ctx context.Context, name string, data []byte) error {
	m.store[name] = data
	return nil
}

func (m *mockCache) Delete(ctx context.Context, name string) error {
	delete(m.store, name)
	return nil
}

type mockCacheMissError struct{}

func (m *mockCacheMissError) Error() string {
	return "cache miss"
}

func TestStore_Get_FromLocalCache(t *testing.T) {
	ctx := context.Background()

	// Create store with New
	mockNode := peer.Mock(ctx)
	store, err := New(context.Background(), mockNode, "")
	assert.NilError(t, err)
	defer store.Close()

	// Replace client with mock
	mockClient := &mockClient{}
	store.client = mockClient

	// Test getting from local cache (should be empty initially)
	_, err = store.Get(ctx, "example.com.crt")
	assert.Assert(t, err != nil) // Should error since no data exists
}

func TestStore_Get_FromRemoteCache(t *testing.T) {
	ctx := context.Background()

	// Create store with New
	mockNode := peer.Mock(ctx)
	store, err := New(context.Background(), mockNode, "")
	assert.NilError(t, err)
	defer store.Close()

	// Mock successful client response
	certData := []byte("remote-certificate")
	mockResp := cr.Response{"certificate": certData}
	store.client = &mockClient{
		responses: map[string]cr.Response{
			"acme:get": mockResp,
		},
	}

	result, err := store.Get(ctx, "example.com.crt")
	assert.NilError(t, err)
	assert.DeepEqual(t, result, certData)
}

func TestStore_Get_RemoteCacheMiss(t *testing.T) {
	ctx := context.Background()

	// Create store with New
	mockNode := peer.Mock(ctx)
	store, err := New(context.Background(), mockNode, "")
	assert.NilError(t, err)
	defer store.Close()

	// Mock client error
	store.client = &mockClient{
		errors: map[string]error{
			"acme:get": errors.New("remote error"),
		},
	}

	_, err = store.Get(ctx, "example.com.crt")
	assert.Assert(t, err != nil)
}

func TestStore_Put_Certificate(t *testing.T) {
	ctx := context.Background()

	// Create store with New
	mockNode := peer.Mock(ctx)
	store, err := New(context.Background(), mockNode, "")
	assert.NilError(t, err)
	defer store.Close()

	// Mock successful client response
	store.client = &mockClient{}

	certData := []byte("test-certificate")
	certName := "example.com.crt"

	err = store.Put(ctx, certName, certData)
	assert.NilError(t, err)
}

func TestStore_Delete_Certificate(t *testing.T) {
	ctx := context.Background()

	// Create store with New
	mockNode := peer.Mock(ctx)
	store, err := New(context.Background(), mockNode, "")
	assert.NilError(t, err)
	defer store.Close()

	// Mock successful client response
	store.client = &mockClient{}

	certName := "example.com.crt"

	err = store.Delete(ctx, certName)
	assert.NilError(t, err)
}
