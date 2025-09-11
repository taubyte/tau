package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

// Helper function to create unique test directory
func createTestDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "patrick-test-*")
	assert.NilError(t, err)
	return dir
}

// Helper function to create test config with private key
func createTestConfig(t *testing.T) *config.Node {
	return &config.Node{
		Root:        createTestDir(t),
		P2PListen:   []string{"/ip4/127.0.0.1/tcp/0"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/0"},
		PrivateKey:  keypair.NewRaw(),
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name          string
		config        *config.Node
		expectedError string
	}{
		{
			name:          "nil config",
			config:        nil,
			expectedError: "building config failed with: you must define p2p port",
		},
		{
			name: "valid config with dev mode",
			config: func() *config.Node {
				config := createTestConfig(t)
				config.DevMode = true
				return config
			}(),
			expectedError: "",
		},
		{
			name: "config with custom database factory",
			config: func() *config.Node {
				config := createTestConfig(t)
				config.Databases = mock.New()
				return config
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

				// Clean up
				if srv != nil {
					srv.Close()
				}
			}
		})
	}
}

func TestNewWithInvalidConfig(t *testing.T) {
	tests := []struct {
		name          string
		config        *config.Node
		expectedError string
	}{
		{
			name: "invalid root path",
			config: &config.Node{
				Root: "", // Invalid empty root
			},
			expectedError: "building config failed with",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			srv, err := New(ctx, tt.config)

			assert.ErrorContains(t, err, tt.expectedError)
			assert.Assert(t, srv == nil)
		})
	}
}

func TestPatrickServiceClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a service
	config := createTestConfig(t)
	config.DevMode = true

	srv, err := New(ctx, config)
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
	config := createTestConfig(t)
	config.DevMode = true

	srv, err := New(ctx, config)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	// Verify dev mode is set
	assert.Assert(t, srv.devMode == true)

	// Clean up
	srv.Close()
}

func TestPatrickServiceWithCustomHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create service with custom HTTP (nil means use auto)
	config := createTestConfig(t)
	config.Http = nil // This should trigger auto.New

	srv, err := New(ctx, config)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	// Clean up
	srv.Close()
}

func TestPatrickServiceInitialization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	config := createTestConfig(t)
	config.DevMode = true

	srv, err := New(ctx, config)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	// Verify all components are initialized
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

	// Create service in dev mode (shorter reannounce time)
	config := createTestConfig(t)
	config.DevMode = true

	srv, err := New(ctx, config)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	// Wait a bit to let the goroutine run
	time.Sleep(2 * time.Second)

	// The goroutine should be running and calling ReannounceJobs
	// We can't easily test the goroutine directly, but we can verify
	// the service is still running and hasn't crashed
	assert.Assert(t, srv != nil)

	// Clean up
	srv.Close()
}

func TestPatrickServiceReannounceJobsAfterClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create service in dev mode (shorter reannounce time)
	config := createTestConfig(t)
	config.DevMode = true

	srv, err := New(ctx, config)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	// Close the service first
	err = srv.Close()
	assert.NilError(t, err)

	// Now try to call ReannounceJobs after close
	// This should handle gracefully without crashing
	err = srv.ReannounceJobs(context.Background())

	// The function might return an error or succeed depending on implementation
	// The key is that it doesn't panic or crash
	// We don't assert a specific error since the behavior might vary
	t.Logf("ReannounceJobs after close returned: %v", err)
}

func TestPatrickServiceGoroutineStopsOnContextCancel(t *testing.T) {
	// Create a context that we can cancel
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Create service in dev mode (shorter reannounce time)
	config := createTestConfig(t)
	config.DevMode = true

	srv, err := New(ctx, config)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	// Wait a bit to let the goroutine start
	time.Sleep(1 * time.Second)

	// Cancel the context - this should stop the goroutine
	cancel()

	// Wait a bit more to see if the goroutine stops
	time.Sleep(1 * time.Second)

	// The service should still be valid but the goroutine should have stopped
	assert.Assert(t, srv != nil)

	// Clean up
	srv.Close()
}

func TestPatrickServiceKV(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	config := createTestConfig(t)

	srv, err := New(ctx, config)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	// Test KV() method
	db := srv.KV()
	assert.Assert(t, db != nil)

	// Clean up
	srv.Close()
}

func TestPatrickServiceNode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	config := createTestConfig(t)

	srv, err := New(ctx, config)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	// Test Node() method
	node := srv.Node()
	assert.Assert(t, node != nil)

	// Clean up
	srv.Close()
}

func TestPatrickServiceWithCustomNode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a mock node
	mockNode := peer.Mock(ctx)

	config := createTestConfig(t)
	config.Node = mockNode

	srv, err := New(ctx, config)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	// Verify the custom node is used
	assert.Assert(t, srv.node == mockNode)

	// Clean up
	srv.Close()
}

func TestPatrickServiceWithCustomClientNode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a mock client node
	mockClientNode := peer.Mock(ctx)

	config := createTestConfig(t)
	config.ClientNode = mockClientNode

	srv, err := New(ctx, config)
	assert.NilError(t, err)
	assert.Assert(t, srv != nil)

	// Clean up
	srv.Close()
}
