package auth

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/config"

	"gotest.tools/v3/assert"
)

func TestAuthService_New(t *testing.T) {
	ctx := context.Background()

	t.Run("successful creation with default config", func(t *testing.T) {
		cfg := newTestConfig(t, 12351)
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		assert.Assert(t, svc != nil)
		defer svc.Close()
	})

	t.Run("successful creation with custom config", func(t *testing.T) {
		cfg := createTestConfig(t, &TestConfig{Port: 12352, NetworkFqdn: "test.tau", DevMode: true})
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		assert.Assert(t, svc != nil)
		defer svc.Close()
	})

	t.Run("successful creation with custom node", func(t *testing.T) {
		cfg := createTestConfig(t, &TestConfig{Port: 12347, UseMockNode: true, CustomKeys: true, NetworkFqdn: "test.tau"})
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		assert.Assert(t, svc != nil)
		defer svc.Close()
	})

	t.Run("successful creation with custom database factory", func(t *testing.T) {
		cfg := createTestConfig(t, &TestConfig{Port: 12348, NetworkFqdn: "test.tau"})
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		assert.Assert(t, svc != nil)
		defer svc.Close()
	})

	t.Run("error with custom node but missing keys", func(t *testing.T) {
		cfg := createTestConfig(t, &TestConfig{Port: 12363, UseMockNode: true, CustomKeys: false, NetworkFqdn: "test.tau"})
		svc, err := New(ctx, cfg)
		assert.Assert(t, err != nil, "Expected error for missing keys")
		assert.Assert(t, svc == nil)
	})

	t.Run("error with invalid config", func(t *testing.T) {
		_, err := config.New(config.WithRoot(""))
		assert.Assert(t, err != nil, "Expected error for invalid config")
	})
}

func TestPackageFunction(t *testing.T) {
	iface := Package()
	assert.Assert(t, iface != nil)

	// Test that it implements the interface
	ctx := context.Background()
	cfg := newTestConfig(t, 12351)

	svc, err := iface.New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, svc != nil)
	defer svc.Close()
}

func TestAuthService_Close(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfig(t, 12353)
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, svc != nil)

	err = svc.Close()
	assert.NilError(t, err)
}

func TestAuthService_Node(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfig(t, 12354)
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, svc != nil)
	defer svc.Close()

	node := svc.Node()
	assert.Assert(t, node != nil)
}

func TestAuthService_KV(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfig(t, 12355)
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, svc != nil)
	defer svc.Close()

	kv := svc.KV()
	assert.Assert(t, kv != nil)
}
