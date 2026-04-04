package auto

import (
	"crypto"
	"crypto/rsa"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/jellydator/ttlcache/v3"
	tns "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/http/options"
	"gotest.tools/v3/assert"
)

// MockSigner is a mock implementation of the crypto.Signer interface

type MockSigner struct{}

func (m *MockSigner) Public() crypto.PublicKey {
	return &rsa.PublicKey{}
}

func (m *MockSigner) Sign(rand io.Reader, digest []byte, opts crypto.SignerOpts) (signature []byte, err error) {
	return nil, nil
}

// MockTNSClient is a mock implementation of the tnsClient interface

type MockTNSClient struct {
	tns.Client
}

func (m *MockTNSClient) Lookup(query tns.Query) (interface{}, error) {
	fmt.Println("Lookup", query, strings.Join(query.Prefix[1:], "."))
	// Mock implementation of Lookup
	if len(query.Prefix) > 0 && strings.HasSuffix(strings.Join(query.Prefix[1:], "."), "com.generated") {
		return []string{"domains", "com", "generated"}, nil
	}

	return nil, fmt.Errorf("domain not found")
}

// Define a mock struct that implements the tns.Object interface
type MockTNSObject struct {
	path tns.Path
}

// Implement the Path method
func (m *MockTNSObject) Path() tns.Path {
	return m.path // Return the path field
}

// Implement the Bind method
func (m *MockTNSObject) Bind(v interface{}) error {
	return nil // Mock implementation
}

// Implement the Interface method
func (m *MockTNSObject) Interface() interface{} {
	fmt.Println("Interface", m.path)
	return map[string]any{"links": []any{"com", "generated"}} // Return a mock interface or nil as needed
}

// Implement the Current method
func (m *MockTNSObject) Current(branch []string) ([]tns.Path, error) {
	return nil, nil // Mock implementation
}

func (m *MockTNSClient) Fetch(path tns.Path) (tns.Object, error) {
	fmt.Println("Fetch", path)
	return &MockTNSObject{path: path}, nil // Return an instance of the mock object with the path field set
}

func TestSetOption(t *testing.T) {
	s := &Service{}
	acmeOption := options.OptionACME{
		DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory",
		Key:          &MockSigner{},
	}
	err := s.SetOption(acmeOption)
	assert.NilError(t, err)
	assert.Equal(t, s.acme.DirectoryURL, acmeOption.DirectoryURL)
	assert.Equal(t, s.acme.Key, acmeOption.Key)
}

func TestIsServiceOrAliasDomain(t *testing.T) {
	cfg, err := config.New(
		config.WithRoot("/tmp"),
		config.WithP2PListen([]string{"/ip4/0.0.0.0/tcp/0"}),
		config.WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithServicesDomainRegExp(regexp.MustCompile(`example.com`)),
		config.WithAliasDomainsRegExp([]*regexp.Regexp{regexp.MustCompile(`alias.com`)}),
	)
	assert.NilError(t, err)
	s := &Service{config: cfg}

	assert.Assert(t, s.isServiceOrAliasDomain("example.com"))
	assert.Assert(t, s.isServiceOrAliasDomain("alias.com"))
	assert.Assert(t, !s.isServiceOrAliasDomain("other.com"))
}

func TestValidateFQDN(t *testing.T) {
	cfg, err := config.New(
		config.WithRoot("/tmp"),
		config.WithP2PListen([]string{"/ip4/0.0.0.0/tcp/0"}),
		config.WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithGeneratedDomainRegExp(regexp.MustCompile(`generated.com`)),
	)
	assert.NilError(t, err)
	s := &Service{
		config:        cfg,
		positiveCache: ttlcache.New(ttlcache.WithTTL[string, bool](PositiveTTL)),
		negativeCache: ttlcache.New(ttlcache.WithTTL[string, bool](NegativeTTL)),
		tnsClient:     &MockTNSClient{},
	}

	err = s.validateFQDN("generated.com")
	assert.NilError(t, err)
}

func TestCustomDomainChecker(t *testing.T) {
	s := &Service{}

	// Mock customDomainChecker function
	mockChecker := func(host string) bool {
		return host == "valid.example.com"
	}

	// Set the mock checker
	s.customDomainChecker = mockChecker

	// Test with a valid domain
	assert.Assert(t, s.customDomainChecker("valid.example.com"))

	// Test with an invalid domain
	assert.Assert(t, !s.customDomainChecker("invalid.example.com"))
}
