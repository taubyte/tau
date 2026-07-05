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
	if len(query.Prefix) > 0 && strings.HasSuffix(strings.Join(query.Prefix[1:], "."), "com.generated") {
		return []string{"domains", "com", "generated"}, nil
	}
	return nil, fmt.Errorf("domain not found")
}

type MockTNSObject struct {
	path tns.Path
}

func (m *MockTNSObject) Path() tns.Path           { return m.path }
func (m *MockTNSObject) Bind(v interface{}) error { return nil }
func (m *MockTNSObject) Current(branch []string) ([]tns.Path, error) {
	return nil, nil
}

func (m *MockTNSObject) Interface() interface{} {
	fmt.Println("Interface", m.path)
	return map[string]any{"links": []any{"com", "generated"}}
}

func (m *MockTNSClient) Fetch(path tns.Path) (tns.Object, error) {
	fmt.Println("Fetch", path)
	return &MockTNSObject{path: path}, nil
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

func TestAutoTrustDomainPredicate(t *testing.T) {
	servicesRe := regexp.MustCompile(`example.com`)
	aliasRe := regexp.MustCompile(`alias.com`)

	s := &Service{
		autoTrustDomain: func(host string) bool {
			host = strings.TrimSuffix(host, ".")
			return servicesRe.MatchString(host) || aliasRe.MatchString(host)
		},
	}

	assert.Assert(t, s.autoTrustDomain("example.com"))
	assert.Assert(t, s.autoTrustDomain("alias.com"))
	assert.Assert(t, !s.autoTrustDomain("other.com"))
}

func TestValidateFQDN(t *testing.T) {
	generatedRe := regexp.MustCompile(`generated.com`)
	s := &Service{
		skipDomainProof: generatedRe.MatchString,
		positiveCache:   ttlcache.New(ttlcache.WithTTL[string, bool](PositiveTTL)),
		negativeCache:   ttlcache.New(ttlcache.WithTTL[string, bool](NegativeTTL)),
		tnsClient:       &MockTNSClient{},
	}

	err := s.validateFQDN("generated.com")
	assert.NilError(t, err)
}

func TestCustomDomainChecker(t *testing.T) {
	s := &Service{}

	mockChecker := func(host string) bool {
		return host == "valid.example.com"
	}

	s.customDomainChecker = mockChecker

	assert.Assert(t, s.customDomainChecker("valid.example.com"))
	assert.Assert(t, !s.customDomainChecker("invalid.example.com"))
}
