package auth

import (
	"context"
	"testing"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb/mock"

	"gotest.tools/v3/assert"
)

func TestAuthService_New(t *testing.T) {
	ctx := context.Background()

	t.Run("successful creation with default config", func(t *testing.T) {
		mockFactory := mock.New()
		cfg := &config.Node{
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12351"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12351"},
			PrivateKey:  keypair.NewRaw(),
			Databases:   mockFactory,
			Root:        t.TempDir(),
		}
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		assert.Assert(t, svc != nil)
		defer svc.Close()
	})

	t.Run("successful creation with custom config", func(t *testing.T) {
		mockFactory := mock.New()
		cfg := &config.Node{
			NetworkFqdn: "test.tau",
			DevMode:     true,
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12352"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12352"},
			PrivateKey:  keypair.NewRaw(),
			Databases:   mockFactory,
			Root:        t.TempDir(),
		}
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		assert.Assert(t, svc != nil)
		defer svc.Close()
	})

	t.Run("successful creation with custom node", func(t *testing.T) {
		mockNode := peer.Mock(ctx)
		cfg := &config.Node{
			NetworkFqdn: "test.tau",
			Node:        mockNode,
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12347"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12347"},
			PrivateKey:  []byte("private-key"),
			DomainValidation: config.DomainValidation{
				PrivateKey: []byte("private-key"),
				PublicKey:  []byte("public-key"),
			},
		}
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		assert.Assert(t, svc != nil)
		defer svc.Close()
	})

	t.Run("successful creation with custom database factory", func(t *testing.T) {
		mockFactory := mock.New()
		cfg := &config.Node{
			NetworkFqdn: "test.tau",
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12348"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12348"},
			PrivateKey:  keypair.NewRaw(),
			Databases:   mockFactory,
			Root:        t.TempDir(),
		}
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		assert.Assert(t, svc != nil)
		defer svc.Close()
	})

	t.Run("error with custom node but missing keys", func(t *testing.T) {
		mockNode := peer.Mock(ctx)
		cfg := &config.Node{
			NetworkFqdn: "test.tau",
			Node:        mockNode,
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12363"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12363"},
			// Missing DomainValidation keys
		}
		svc, err := New(ctx, cfg)
		assert.Assert(t, err != nil, "Expected error for missing keys")
		assert.Assert(t, svc == nil)
	})

	t.Run("error with invalid config", func(t *testing.T) {
		cfg := &config.Node{
			NetworkFqdn: "", // Invalid empty FQDN
		}
		svc, err := New(ctx, cfg)
		assert.Assert(t, err != nil, "Expected error for invalid config")
		assert.Assert(t, svc == nil)
	})
}

func TestPackageFunction(t *testing.T) {
	iface := Package()
	assert.Assert(t, iface != nil)

	// Test that it implements the interface
	ctx := context.Background()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12351"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12351"},
		PrivateKey:  keypair.NewRaw(),
		Root:        t.TempDir(),
	}

	svc, err := iface.New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, svc != nil)
	defer svc.Close()
}

func TestAuthService_Close(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12353"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12353"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, svc != nil)

	err = svc.Close()
	assert.NilError(t, err)
}

func TestAuthService_Node(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12354"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12354"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, svc != nil)
	defer svc.Close()

	node := svc.Node()
	assert.Assert(t, node != nil)
}

func TestAuthService_KV(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12355"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12355"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, svc != nil)
	defer svc.Close()

	kv := svc.KV()
	assert.Assert(t, kv != nil)
}
