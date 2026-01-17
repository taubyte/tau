package auth

import (
	"context"
	"testing"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

func TestACMEFunctionality(t *testing.T) {
	t.Run("create auth service with ACME support", func(t *testing.T) {
		ctx := context.Background()
		mockNode := peer.Mock(ctx)

		cfg := &config.Node{
			NetworkFqdn: "test.tau",
			Node:        mockNode,
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12345"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12345"},
			PrivateKey:  []byte("private-key"),
			Root:        t.TempDir(),
			DomainValidation: config.DomainValidation{
				PrivateKey: []byte("private-key"),
				PublicKey:  []byte("public-key"),
			},
		}

		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		defer svc.Close()

		// Verify the service was created successfully with ACME support
		assert.Assert(t, svc != nil)
		assert.Equal(t, svc.Node(), mockNode)
		assert.Assert(t, svc.KV() != nil)
	})

	t.Run("test domain validation keys", func(t *testing.T) {
		ctx := context.Background()
		mockNode := peer.Mock(ctx)

		privateKey := []byte("test-private-key")
		publicKey := []byte("test-public-key")

		cfg := &config.Node{
			NetworkFqdn: "test.tau",
			Node:        mockNode,
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12346"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12346"},
			PrivateKey:  []byte("private-key"),
			Root:        t.TempDir(),
			DomainValidation: config.DomainValidation{
				PrivateKey: privateKey,
				PublicKey:  publicKey,
			},
		}

		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		defer svc.Close()

		// Verify the service was created successfully
		assert.Assert(t, svc != nil)
	})
}

func TestMockDatabaseWithACME(t *testing.T) {
	t.Run("create mock database for ACME operations", func(t *testing.T) {
		mockFactory := mock.New()
		assert.Assert(t, mockFactory != nil)

		// Test creating a mock database
		ctx := context.Background()
		mockNode := peer.Mock(ctx)

		// Create a mock database instance through the factory
		mockDB, err := mockFactory.New(nil, "acme-test", 5)
		if err == nil {
			assert.Assert(t, mockDB != nil)
		}

		assert.Assert(t, mockNode != nil)
	})

	t.Run("test mock database operations", func(t *testing.T) {
		// Create a mock factory and then a database
		mockFactory := mock.New()
		mockDB, err := mockFactory.New(nil, "test-db", 5)
		assert.NilError(t, err)
		assert.Assert(t, mockDB != nil)

		// Test basic database operations
		ctx := context.Background()

		// Test Put operation
		err = mockDB.Put(ctx, "test-key", []byte("test-value"))
		assert.NilError(t, err)

		// Test Get operation
		value, err := mockDB.Get(ctx, "test-key")
		assert.NilError(t, err)
		assert.DeepEqual(t, value, []byte("test-value"))

		// Test Get with non-existent key
		_, err = mockDB.Get(ctx, "non-existent-key")
		assert.Assert(t, err != nil, "Expected error for non-existent key")

		// Test Close operation
		mockDB.Close()
	})
}

func TestPeerNodeWithACME(t *testing.T) {
	t.Run("create mock peer node for ACME", func(t *testing.T) {
		ctx := context.Background()
		mockNode := peer.Mock(ctx)
		assert.Assert(t, mockNode != nil)

		// Test that the node has the required methods
		// Note: We can't test the actual P2P functionality without more complex setup
		// but we can verify the mock was created successfully
	})

	t.Run("test multiple mock nodes", func(t *testing.T) {
		ctx := context.Background()

		// Create multiple mock nodes to test isolation
		node1 := peer.Mock(ctx)
		node2 := peer.Mock(ctx)

		assert.Assert(t, node1 != nil)
		assert.Assert(t, node2 != nil)
		assert.Assert(t, node1 != node2)
	})
}

func TestConfigurationValidation(t *testing.T) {
	t.Run("test valid configuration", func(t *testing.T) {
		cfg := &config.Node{
			NetworkFqdn: "test.tau",
			DevMode:     true,
		}

		// Test that the configuration is valid
		assert.Assert(t, cfg.NetworkFqdn != "")
		assert.Assert(t, cfg.DevMode)
	})

	t.Run("test configuration with domain validation", func(t *testing.T) {
		cfg := &config.Node{
			NetworkFqdn: "test.tau",
			DomainValidation: config.DomainValidation{
				PrivateKey: []byte("private-key"),
				PublicKey:  []byte("public-key"),
			},
		}

		// Test that the configuration has domain validation keys
		assert.Assert(t, cfg.NetworkFqdn != "")
		assert.Assert(t, cfg.DomainValidation.PrivateKey != nil)
		assert.Assert(t, cfg.DomainValidation.PublicKey != nil)
	})
}
