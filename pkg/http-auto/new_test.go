package auto

import (
	"context"
	"crypto"
	"testing"

	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/config"
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

func testConfig(t *testing.T, opts ...config.Option) config.Config {
	base := []config.Option{
		config.WithRoot("/tmp"),
		config.WithP2PListen([]string{"/ip4/0.0.0.0/tcp/0"}),
		config.WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithPrivateKey(make([]byte, 32)),
	}
	cfg, err := config.New(append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestNew(t *testing.T) {
	ctx := context.Background()
	mockNode := peer.Mock(ctx)

	t.Run("Clients and caches are not nil", func(t *testing.T) {
		mockConfig := testConfig(t,
			config.WithDevMode(false),
			config.WithEnableHTTPS(true),
			config.WithHttpListen("127.0.0.1:443"),
			config.WithCustomAcme(true),
			config.WithAcmeUrl("https://acme-staging-v02.api.letsencrypt.org/directory"),
			config.WithAcmeKey(&MockSigner{}),
			config.WithVerbose(false),
		)
		service, err := New(ctx, mockNode, mockConfig)
		assert.NilError(t, err)
		assert.Assert(t, service != nil)

		assert.Assert(t, service.(*Service).certStore != nil)
		assert.Assert(t, service.(*Service).authClient != nil)
		assert.Assert(t, service.(*Service).tnsClient != nil)
		assert.Assert(t, service.(*Service).positiveCache != nil)
		assert.Assert(t, service.(*Service).negativeCache != nil)
	})

	t.Run("DevMode with HTTPS", func(t *testing.T) {
		mockConfig := testConfig(t, config.WithDevMode(true), config.WithEnableHTTPS(true), config.WithHttpListen("127.0.0.1:443"))
		service, err := New(ctx, mockNode, mockConfig)
		assert.NilError(t, err)
		assert.Assert(t, service != nil)
	})

	t.Run("Production mode", func(t *testing.T) {
		mockConfig := testConfig(t, config.WithDevMode(false), config.WithHttpListen("127.0.0.1:443"))
		service, err := New(ctx, mockNode, mockConfig)
		assert.NilError(t, err)
		assert.Assert(t, service != nil)
	})

	t.Run("DevMode without HTTPS", func(t *testing.T) {
		mockConfig := testConfig(t, config.WithDevMode(true), config.WithEnableHTTPS(false))
		service, err := New(ctx, mockNode, mockConfig)
		assert.NilError(t, err)
		assert.Assert(t, service != nil)
	})

	t.Run("ACME options", func(t *testing.T) {
		mockConfig := testConfig(t,
			config.WithCustomAcme(true),
			config.WithAcmeUrl("https://acme-staging-v02.api.letsencrypt.org/directory"),
			config.WithAcmeKey(&MockSigner{}),
		)
		mockConfigurable := &MockConfigurable{}
		for _, op := range opsFromConfig(mockConfig) {
			err := op(mockConfigurable)
			assert.NilError(t, err)
		}
		assert.Equal(t, mockConfigurable.acmeUrl, "https://acme-staging-v02.api.letsencrypt.org/directory")
		assert.Equal(t, mockConfigurable.acmeKey, &MockSigner{})
	})

	t.Run("ACME enabled", func(t *testing.T) {
		mockConfig := testConfig(t,
			config.WithDevMode(false),
			config.WithEnableHTTPS(true),
			config.WithHttpListen("127.0.0.1:443"),
			config.WithCustomAcme(true),
			config.WithAcmeUrl("https://acme-staging-v02.api.letsencrypt.org/directory"),
			config.WithAcmeKey(&MockSigner{}),
		)
		service, err := New(ctx, mockNode, mockConfig)
		assert.NilError(t, err)
		assert.Assert(t, service != nil)

		mockConfigurable := &MockConfigurable{}
		for _, op := range opsFromConfig(mockConfig) {
			err := op(mockConfigurable)
			assert.NilError(t, err)
		}
		assert.Equal(t, mockConfigurable.acmeUrl, "https://acme-staging-v02.api.letsencrypt.org/directory")
		assert.Equal(t, mockConfigurable.acmeKey, &MockSigner{})
	})

	t.Run("DevMode without HTTPS", func(t *testing.T) {
		mockConfig := testConfig(t, config.WithDevMode(true), config.WithEnableHTTPS(false))
		service, err := New(ctx, mockNode, mockConfig)
		assert.NilError(t, err)
		assert.Assert(t, service != nil)
	})
}
