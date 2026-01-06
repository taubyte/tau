//go:build vagrant && linux

package containerd

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

// TestContainerdBackend_Vagrant_RootfulMode tests rootful mode using containerd in Vagrant VM
// This test runs inside the VM where containerd is running
func TestContainerdBackend_Vagrant_RootfulMode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create backend pointing to local containerd socket (running in this VM)
	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,                             // Don't start - it's already running as system service
		SocketPath:   "/run/containerd/containerd.sock", // Local socket in VM
		Namespace:    "tau-test-vagrant",
	})

	require.NoError(t, err, "Backend creation should succeed")
	assert.NotNil(t, backend.client, "Client should be initialized")
	assert.Nil(t, backend.daemon, "Daemon should not be initialized (using system containerd)")

	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

	// Test socket connection
	err = backend.TestSocketConnection()
	assert.NoError(t, err, "Socket connection should work")

	// Test getting version
	version, err := backend.client.Version(backend.client.ctx)
	assert.NoError(t, err, "Should be able to get containerd version")
	assert.NotEmpty(t, version.Version, "Version should not be empty")
	t.Logf("Connected to containerd version: %s", version.Version)

	// Test container operations
	containerConfig := &core.ContainerConfig{
		Image:   "quay.io/libpod/alpine:latest",
		Command: []string{"echo", "hello from vagrant test"},
	}

	containerID, err := backend.Create(context.Background(), containerConfig)
	assert.NoError(t, err, "Container creation should succeed")
	assert.NotEmpty(t, containerID, "Container ID should not be empty")

	_, err = backend.Inspect(context.Background(), containerID)
	assert.NoError(t, err, "Container inspection should succeed")

	err = backend.Remove(context.Background(), containerID)
	assert.NoError(t, err, "Container removal should succeed")
}

// TestContainerdBackend_Vagrant_ContainerOperations tests all container operations using Vagrant
// This test runs inside the VM where containerd is running
func TestContainerdBackend_Vagrant_ContainerOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		SocketPath:   "/run/containerd/containerd.sock", // Local socket in VM
		Namespace:    "tau-test-vagrant-ops",
	})

	require.NoError(t, err)
	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

	// Test multiple container operations
	tests := []struct {
		name             string
		command          []string
		expect           string
		expectedExitCode int
	}{
		{
			name:             "SimpleEcho",
			command:          []string{"echo", "test output"},
			expect:           "test output",
			expectedExitCode: 0,
		},
		{
			name:             "ExitCode",
			command:          []string{"sh", "-c", "exit 42"},
			expect:           "",
			expectedExitCode: 42,
		},
		{
			name:             "MultiLine",
			command:          []string{"sh", "-c", "echo 'line1'; echo 'line2'"},
			expect:           "line1",
			expectedExitCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containerConfig := &core.ContainerConfig{
				Image:   "quay.io/libpod/alpine:latest",
				Command: tt.command,
			}

			containerID, err := backend.Create(context.Background(), containerConfig)
			require.NoError(t, err)

			err = backend.Start(context.Background(), containerID)
			require.NoError(t, err)

			err = backend.Wait(context.Background(), containerID)
			// Wait may return an error for non-zero exit codes, which is expected
			// We'll check the exit code via Inspect instead
			if tt.expectedExitCode != 0 {
				// Non-zero exit is expected, error from Wait is OK
			} else {
				require.NoError(t, err)
			}

			if tt.expect != "" {
				logs, err := backend.Logs(context.Background(), containerID)
				require.NoError(t, err)
				defer logs.Close()

				logData, err := io.ReadAll(logs)
				require.NoError(t, err)

				assert.Contains(t, string(logData), tt.expect)
			}

			// Check exit code
			status, err := backend.Inspect(context.Background(), containerID)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedExitCode, status.ExitCode, "Exit code should match")

			err = backend.Remove(context.Background(), containerID)
			assert.NoError(t, err, "Container removal should succeed")
		})
	}
}

// TestContainerdBackend_Vagrant_RootlessMode tests rootless mode using containerd in Vagrant VM
// This test runs inside the VM where rootless containerd will be started
func TestContainerdBackend_Vagrant_RootlessMode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create backend with rootless mode enabled and AutoStart
	// Socket path will be auto-detected (XDG_RUNTIME_DIR/tau/containerd/containerd.sock)
	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
		AutoStart:    true, // Start rootless containerd automatically
		Namespace:    "tau-test-vagrant-rootless",
	})

	require.NoError(t, err, "Backend creation should succeed")
	assert.NotNil(t, backend.client, "Client should be initialized")
	assert.NotNil(t, backend.daemon, "Daemon should be initialized (rootless containerd)")

	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
		// Stop rootless containerd daemon if it was started
		if backend != nil && backend.daemon != nil {
			backend.daemon.Stop(context.Background())
		}
	}()

	// Test socket connection
	err = backend.TestSocketConnection()
	assert.NoError(t, err, "Socket connection should work")

	// Test getting version
	version, err := backend.client.Version(backend.client.ctx)
	assert.NoError(t, err, "Should be able to get containerd version")
	assert.NotEmpty(t, version.Version, "Version should not be empty")
	t.Logf("Connected to rootless containerd version: %s", version.Version)

	// Test container operations
	containerConfig := &core.ContainerConfig{
		Image:   "quay.io/libpod/alpine:latest",
		Command: []string{"echo", "hello from rootless vagrant test"},
	}

	containerID, err := backend.Create(context.Background(), containerConfig)
	assert.NoError(t, err, "Container creation should succeed")
	assert.NotEmpty(t, containerID, "Container ID should not be empty")

	_, err = backend.Inspect(context.Background(), containerID)
	assert.NoError(t, err, "Container inspection should succeed")

	err = backend.Remove(context.Background(), containerID)
	assert.NoError(t, err, "Container removal should succeed")
}

// TestContainerdBackend_Vagrant_RootlessContainerOperations tests all container operations in rootless mode using Vagrant
// This test runs inside the VM where rootless containerd will be started
func TestContainerdBackend_Vagrant_RootlessContainerOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
		AutoStart:    true, // Start rootless containerd automatically
		Namespace:    "tau-test-vagrant-rootless-ops",
	})

	require.NoError(t, err)
	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
		// Stop rootless containerd daemon if it was started
		if backend != nil && backend.daemon != nil {
			backend.daemon.Stop(context.Background())
		}
	}()

	// Test multiple container operations
	tests := []struct {
		name             string
		command          []string
		expect           string
		expectedExitCode int
	}{
		{
			name:             "SimpleEcho",
			command:          []string{"echo", "test output rootless"},
			expect:           "test output rootless",
			expectedExitCode: 0,
		},
		{
			name:             "ExitCode",
			command:          []string{"sh", "-c", "exit 42"},
			expect:           "",
			expectedExitCode: 42,
		},
		{
			name:             "MultiLine",
			command:          []string{"sh", "-c", "echo 'line1'; echo 'line2'"},
			expect:           "line1",
			expectedExitCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containerConfig := &core.ContainerConfig{
				Image:   "quay.io/libpod/alpine:latest",
				Command: tt.command,
			}

			containerID, err := backend.Create(context.Background(), containerConfig)
			require.NoError(t, err)

			err = backend.Start(context.Background(), containerID)
			require.NoError(t, err)

			err = backend.Wait(context.Background(), containerID)
			// Wait may return an error for non-zero exit codes, which is expected
			// We'll check the exit code via Inspect instead
			if tt.expectedExitCode != 0 {
				// Non-zero exit is expected, error from Wait is OK
			} else {
				require.NoError(t, err)
			}

			if tt.expect != "" {
				logs, err := backend.Logs(context.Background(), containerID)
				require.NoError(t, err)
				defer logs.Close()

				logData, err := io.ReadAll(logs)
				require.NoError(t, err)

				assert.Contains(t, string(logData), tt.expect)
			}

			// Check exit code
			status, err := backend.Inspect(context.Background(), containerID)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedExitCode, status.ExitCode, "Exit code should match")

			err = backend.Remove(context.Background(), containerID)
			assert.NoError(t, err, "Container removal should succeed")
		})
	}
}
