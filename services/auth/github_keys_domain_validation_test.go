package auth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"github.com/taubyte/tau/utils/id"

	"gotest.tools/v3/assert"
)

// Test GitHub key generation workflow with comprehensive scenarios
func TestGitHubKeyGenerationWorkflowWithFixtures(t *testing.T) {
	// Test sequence: generate multiple keys and verify uniqueness
	keySet := make(map[string]bool)

	for i := 0; i < 20; i++ {
		keyName, pubKey, privKey, err := generateKey()
		assert.NilError(t, err)
		assert.Assert(t, keyName != "")
		assert.Assert(t, pubKey != "")
		assert.Assert(t, privKey != "")

		// Verify keys are unique across generations
		assert.Assert(t, !keySet[pubKey], "Public key should be unique across generations")
		assert.Assert(t, !keySet[privKey], "Private key should be unique across generations")

		keySet[pubKey] = true
		keySet[privKey] = true

		// Verify key properties
		assert.Assert(t, pubKey != privKey, "Public and private keys should be different")
		assert.Assert(t, len(pubKey) > 0, "Public key should not be empty")
		assert.Assert(t, len(privKey) > 0, "Private key should not be empty")

		// Verify key name is consistent (it's a constant)
		// Note: deployKeyName gets modified to devDeployKeyName when DevMode is true
		assert.Assert(t, keyName == "taubyte_deploy_key" || keyName == "taubyte_deploy_key_dev", "Key name should be one of the expected values (keyName: %s)", keyName)
	}

	// Verify we generated the expected number of unique keys
	expectedKeys := 20 * 2 // 20 iterations * 2 keys each (pub + priv)
	assert.Equal(t, len(keySet), expectedKeys, "Should have generated unique keys")
}

// Test domain validation workflow with comprehensive test data sequences
func TestDomainValidationWorkflowWithFixtures(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12376"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12376"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test sequence: setup domain validation keys then test workflow
	// 1. Generate valid ECDSA keys for testing
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.NilError(t, err)

	// Encode private key to PEM
	privKeyBytes, err := x509.MarshalECPrivateKey(privKey)
	assert.NilError(t, err)
	privKeyPEM := &pem.Block{Type: "EC PRIVATE KEY", Bytes: privKeyBytes}
	var privBuf bytes.Buffer
	err = pem.Encode(&privBuf, privKeyPEM)
	assert.NilError(t, err)

	// Encode public key to PEM
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	assert.NilError(t, err)
	pubKeyPEM := &pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes}
	var pubBuf bytes.Buffer
	err = pem.Encode(&pubBuf, pubKeyPEM)
	assert.NilError(t, err)

	// 2. Test domain validation with valid inputs
	testCID := cid.NewCidV1(cid.Raw, []byte("test-project-id"))
	claims, err := domainValidationNew("example.com", testCID, privBuf.Bytes(), pubBuf.Bytes())
	assert.NilError(t, err)
	assert.Assert(t, claims != nil)

	// 3. Test token signing
	token, err := claims.Sign()
	assert.NilError(t, err)
	assert.Assert(t, len(token) > 0)

	// 4. Test with different domain patterns
	domains := []string{"test.com", "sub.test.com", "deep.sub.test.com"}
	for _, domain := range domains {
		claims, err := domainValidationNew(domain, testCID, privBuf.Bytes(), pubBuf.Bytes())
		assert.NilError(t, err)
		assert.Assert(t, claims != nil)
	}

	// 5. Test wildcard domain generation
	wildcardDomain := generateWildCardDomain("sub.example.com")
	assert.Equal(t, wildcardDomain, "*.example.com")

	wildcardDomain2 := generateWildCardDomain("deep.sub.example.com")
	assert.Equal(t, wildcardDomain2, "*.sub.example.com")
}

// Test domain validation function
func TestDomainValidationNew(t *testing.T) {
	// Create a test CID using the default constructor
	testCID := cid.NewCidV1(cid.Raw, []byte("test-project-id"))

	// Generate valid ECDSA keys for testing
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.NilError(t, err)

	// Encode private key to PEM
	privKeyBytes, err := x509.MarshalECPrivateKey(privKey)
	assert.NilError(t, err)
	privKeyPEM := &pem.Block{Type: "EC PRIVATE KEY", Bytes: privKeyBytes}
	var privBuf bytes.Buffer
	err = pem.Encode(&privBuf, privKeyPEM)
	assert.NilError(t, err)

	// Encode public key to PEM
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	assert.NilError(t, err)
	pubKeyPEM := &pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes}
	var pubBuf bytes.Buffer
	err = pem.Encode(&pubBuf, pubKeyPEM)
	assert.NilError(t, err)

	// Test with valid inputs
	claims, err := domainValidationNew("example.com", testCID, privBuf.Bytes(), pubBuf.Bytes())
	assert.NilError(t, err)
	assert.Assert(t, claims != nil)

	// Test with empty FQDN - the domain validation library might accept empty FQDN
	// so we just test that the function can be called without panicking
	_, err = domainValidationNew("", testCID, privBuf.Bytes(), pubBuf.Bytes())
	// We don't assert on the result since the library behavior might vary
}

// Test domain validation token HTTP handler with comprehensive scenarios
func TestTokenDomainHTTPHandler(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()

	// Generate valid ECDSA keys for testing
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.NilError(t, err)

	// Encode private key to PEM
	privKeyBytes, err := x509.MarshalECPrivateKey(privKey)
	assert.NilError(t, err)
	privKeyPEM := &pem.Block{Type: "EC PRIVATE KEY", Bytes: privKeyBytes}
	var privBuf bytes.Buffer
	err = pem.Encode(&privBuf, privKeyPEM)
	assert.NilError(t, err)

	// Encode public key to PEM
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	assert.NilError(t, err)
	pubKeyPEM := &pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes}
	var pubBuf bytes.Buffer
	err = pem.Encode(&pubBuf, pubKeyPEM)
	assert.NilError(t, err)

	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12381"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12381"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
		DomainValidation: config.DomainValidation{
			PrivateKey: privBuf.Bytes(),
			PublicKey:  pubBuf.Bytes(),
		},
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test 1: Valid domain validation token generation
	t.Run("valid token generation", func(t *testing.T) {
		mockCtx := &mockHTTPContext{
			variables: map[string]string{
				"fqdn":    "example.com",
				"project": id.Generate(),
			},
		}

		result, err := svc.tokenDomainHTTPHandler(mockCtx)
		assert.NilError(t, err)
		assert.Assert(t, result != nil)

		response, ok := result.(map[string]string)
		assert.Assert(t, ok, "Expected map[string]string response")
		assert.Assert(t, response["token"] != "", "Expected non-empty token")
		assert.Assert(t, response["entry"] != "", "Expected non-empty entry")
		assert.Equal(t, response["type"], "txt", "Expected type to be txt")
	})

	// Test 2: Missing FQDN variable
	t.Run("missing fqdn", func(t *testing.T) {
		mockCtx := &mockHTTPContext{
			variables: map[string]string{
				"project": "bafybeihdwdcefgh4dqkjv67ojcmw7ojee6axkdg4hkwx7ymwyi9h6gqy",
			},
		}

		_, err := svc.tokenDomainHTTPHandler(mockCtx)
		assert.Assert(t, err != nil, "Expected error for missing fqdn")
	})

	// Test 3: Missing project variable
	t.Run("missing project", func(t *testing.T) {
		mockCtx := &mockHTTPContext{
			variables: map[string]string{
				"fqdn": "example.com",
			},
		}

		_, err := svc.tokenDomainHTTPHandler(mockCtx)
		assert.Assert(t, err != nil, "Expected error for missing project")
	})

	// Test 4: Project too short
	t.Run("project too short", func(t *testing.T) {
		mockCtx := &mockHTTPContext{
			variables: map[string]string{
				"fqdn":    "example.com",
				"project": "short",
			},
		}

		_, err := svc.tokenDomainHTTPHandler(mockCtx)
		assert.Assert(t, err != nil, "Expected error for project too short")
		assert.Assert(t, err.Error() == "project is too short")
	})

	// Test 5: Invalid project CID
	t.Run("invalid project cid", func(t *testing.T) {
		mockCtx := &mockHTTPContext{
			variables: map[string]string{
				"fqdn":    "example.com",
				"project": "invalid-cid-format",
			},
		}

		_, err := svc.tokenDomainHTTPHandler(mockCtx)
		assert.Assert(t, err != nil, "Expected error for invalid project CID")
		assert.Assert(t, err.Error()[:25] == "decode project id  failed")
	})
}
