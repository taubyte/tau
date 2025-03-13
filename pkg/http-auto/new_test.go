package auto

import (
	"context"
	"crypto"
	"testing"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/http/options"
	"gotest.tools/v3/assert"
)

// Add a mock Configurable implementation to capture and verify ACME options

type MockConfigurable struct {
	acmeUrl string
	acmeKey crypto.Signer
}

func (m *MockConfigurable) SetOption(option any) error {
	if acmeOption, ok := option.(options.OptionACME); ok {
		m.acmeUrl = acmeOption.DirectoryURL
		m.acmeKey = acmeOption.Key
	}
	return nil
}

func TestNew(t *testing.T) {
	ctx := context.Background()
	mockNode := peer.Mock(ctx) // Use the provided mock node function
	mockConfig := &config.Node{
		DevMode:     false,
		EnableHTTPS: true,
		HttpListen:  "127.0.0.1:443",
		CustomAcme:  true,
		AcmeUrl:     "https://acme-staging-v02.api.letsencrypt.org/directory",
		AcmeKey:     &MockSigner{}, // Use mock signer
		Verbose:     false,
	}

	t.Run("Clients and caches are not nil", func(t *testing.T) {
		service, err := New(ctx, mockNode, mockConfig)
		assert.NilError(t, err)
		assert.Assert(t, service != nil)

		assert.Assert(t, service.(*Service).certStore != nil)
		assert.Assert(t, service.(*Service).authClient != nil)
		assert.Assert(t, service.(*Service).tnsClient != nil)
		assert.Assert(t, service.(*Service).positiveCache != nil)
		assert.Assert(t, service.(*Service).negativeCache != nil)
	})

	mockConfig.DevMode = true

	t.Run("DevMode with HTTPS", func(t *testing.T) {
		service, err := New(ctx, mockNode, mockConfig)
		assert.NilError(t, err)
		assert.Assert(t, service != nil)
	})

	mockConfig.DevMode = false
	t.Run("Production mode", func(t *testing.T) {
		service, err := New(ctx, mockNode, mockConfig)
		assert.NilError(t, err)
		assert.Assert(t, service != nil)
	})

	mockConfig.DevMode = true
	mockConfig.EnableHTTPS = false
	t.Run("DevMode without HTTPS", func(t *testing.T) {
		service, err := New(ctx, mockNode, mockConfig)
		assert.NilError(t, err)
		assert.Assert(t, service != nil)
	})

	mockConfig.DevMode = false
	mockConfig.CustomAcme = true
	mockConfig.AcmeUrl = "https://acme-staging-v02.api.letsencrypt.org/directory"
	mockConfig.AcmeKey = &MockSigner{} // Use mock signer

	t.Run("ACME options", func(t *testing.T) {
		mockConfigurable := &MockConfigurable{}
		for _, op := range opsFromConfig(mockConfig) {
			err := op(mockConfigurable)
			assert.NilError(t, err)
		}
		assert.Equal(t, mockConfigurable.acmeUrl, "https://acme-staging-v02.api.letsencrypt.org/directory")
		assert.Equal(t, mockConfigurable.acmeKey, &MockSigner{})
	})

	t.Run("ACME enabled", func(t *testing.T) {
		service, err := New(ctx, mockNode, mockConfig)
		assert.NilError(t, err)
		assert.Assert(t, service != nil)

		// Verify ACME options
		mockConfigurable := &MockConfigurable{}
		for _, op := range opsFromConfig(mockConfig) {
			err := op(mockConfigurable)
			assert.NilError(t, err)
		}
		assert.Equal(t, mockConfigurable.acmeUrl, "https://acme-staging-v02.api.letsencrypt.org/directory")
		assert.Equal(t, mockConfigurable.acmeKey, &MockSigner{})
	})

	// Reset CustomAcme for other tests
	mockConfig.CustomAcme = false

	// Existing test cases
	mockConfig.DevMode = true
	mockConfig.EnableHTTPS = false
	t.Run("DevMode without HTTPS", func(t *testing.T) {
		service, err := New(ctx, mockNode, mockConfig)
		assert.NilError(t, err)
		assert.Assert(t, service != nil)
	})
}
