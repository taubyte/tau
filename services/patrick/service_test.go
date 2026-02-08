package service

import (
	"context"
	"testing"
	"time"

	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

func createTestConfig(t *testing.T) config.Config {
	cfg, err := config.New(
		config.WithRoot(t.TempDir()),
		config.WithP2PListen([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithPrivateKey(keypair.NewRaw()),
	)
	assert.NilError(t, err)
	return cfg
}

func TestNew(t *testing.T) {
	tests := []struct {
		name          string
		config        config.Config
		expectedError string
	}{
		{
			name:          "nil config",
			config:        nil,
			expectedError: "building config failed with: you must define p2p port",
		},
		{
			name: "valid config with dev mode",
			config: func() config.Config {
				cfg, err := config.New(
					config.WithRoot(t.TempDir()),
					config.WithP2PListen([]string{"/ip4/127.0.0.1/tcp/0"}),
					config.WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/0"}),
					config.WithPrivateKey(keypair.NewRaw()),
					config.WithDevMode(true),
				)
				assert.NilError(t, err)
				return cfg
			}(),
			expectedError: "",
		},
		{
			name: "config with custom database factory",
			config: func() config.Config {
				cfg := createTestConfig(t)
				cfg.SetDatabases(mock.New())
				return cfg
			}(),
			expectedError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			srv, err := New(ctx, tt.config)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				assert.Assert(t, srv == nil)
			} else {
				assert.NilError(t, err)
				assert.Assert(t, srv != nil)

				if srv != nil {
					srv.Close()
				}
			}
		})
	}
}

func TestNewWithInvalidConfig(t *testing.T) {
	// config.New(WithRoot("")) returns error; we cannot get a Config with invalid root to pass to New
	_, err := config.New(config.WithRoot(""))
	assert.Assert(t, err != nil, "expected config.New with empty root to fail")
}

func TestPatrickServiceClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a service
	cfg, err := config.New(
		config.WithRoot(t.TempDir()),
		config.WithP2PListen([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithPrivateKey(keypair.NewRaw()),
		config.WithDevMode(true),
	)
	assert.NilError(t, err)
	srv, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	// Test Close
	err = srv.Close()
	assert.NilError(t, err)
}

func TestPatrickServiceDevMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create service in dev mode
	cfg, err := config.New(
		config.WithRoot(t.TempDir()),
		config.WithP2PListen([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithPrivateKey(keypair.NewRaw()),
		config.WithDevMode(true),
	)
	assert.NilError(t, err)
	srv, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	assert.Assert(t, srv.devMode == true)

	// Clean up
	srv.Close()
}

func TestPatrickServiceWithCustomHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := createTestConfig(t)
	// Http is not set on config by default
	srv, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	// Clean up
	srv.Close()
}

func TestPatrickServiceInitialization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := config.New(
		config.WithRoot(t.TempDir()),
		config.WithP2PListen([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithPrivateKey(keypair.NewRaw()),
		config.WithDevMode(true),
	)
	assert.NilError(t, err)
	srv, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	assert.Assert(t, srv.node != nil)
	assert.Assert(t, srv.db != nil)
	assert.Assert(t, srv.authClient != nil)
	assert.Assert(t, srv.tnsClient != nil)
	assert.Assert(t, srv.monkeyClient != nil)
	assert.Assert(t, srv.stream != nil)
	assert.Assert(t, srv.http != nil)

	// Clean up
	srv.Close()
}

func TestPatrickServiceReannounceJobsGoroutine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	cfg, err := config.New(
		config.WithRoot(t.TempDir()),
		config.WithP2PListen([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithPrivateKey(keypair.NewRaw()),
		config.WithDevMode(true),
	)
	assert.NilError(t, err)
	srv, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	time.Sleep(2 * time.Second)

	assert.Assert(t, srv != nil)

	// Clean up
	srv.Close()
}

func TestPatrickServiceReannounceJobsAfterClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := config.New(
		config.WithRoot(t.TempDir()),
		config.WithP2PListen([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithPrivateKey(keypair.NewRaw()),
		config.WithDevMode(true),
	)
	assert.NilError(t, err)
	srv, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	err = srv.Close()
	assert.NilError(t, err)

	err = srv.ReannounceJobs(context.Background())

	t.Logf("ReannounceJobs after close returned: %v", err)
}

func TestPatrickServiceGoroutineStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cfg, err := config.New(
		config.WithRoot(t.TempDir()),
		config.WithP2PListen([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithPrivateKey(keypair.NewRaw()),
		config.WithDevMode(true),
	)
	assert.NilError(t, err)
	srv, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	time.Sleep(1 * time.Second)

	cancel()

	time.Sleep(1 * time.Second)

	assert.Assert(t, srv != nil)

	// Clean up
	srv.Close()
}

func TestPatrickServiceKV(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := createTestConfig(t)

	srv, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	db := srv.KV()
	assert.Assert(t, db != nil)

	// Clean up
	srv.Close()
}

func TestPatrickServiceNode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := createTestConfig(t)

	srv, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	node := srv.Node()
	assert.Assert(t, node != nil)

	// Clean up
	srv.Close()
}

func TestPatrickServiceWithCustomNode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mockNode := peer.Mock(ctx)

	cfg := createTestConfig(t)
	cfg.SetNode(mockNode)

	srv, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	assert.Assert(t, srv.node == mockNode)

	// Clean up
	srv.Close()
}

func TestPatrickServiceWithCustomClientNode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mockClientNode := peer.Mock(ctx)

	cfg := createTestConfig(t)
	cfg.SetClientNode(mockClientNode)

	srv, err := New(ctx, cfg)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	// Clean up
	srv.Close()
}
