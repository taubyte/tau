package containerd

import (
	"context"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/taubyte/tau/pkg/containers"
)

func TestContainerdBackend_detectRootlessMode(t *testing.T) {
	// Test auto-detection
	config := containers.ContainerdConfig{}

	backend := &ContainerdBackend{
		config: config,
	}

	err := backend.detectRootlessMode()
	if err != nil {
		t.Fatalf("detectRootlessMode failed: %v", err)
	}

	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() failed: %v", err)
	}

	isRoot := currentUser.Uid == "0"
	var expectedRootless containers.RootlessMode
	if isRoot {
		expectedRootless = containers.RootlessModeDisabled
	} else {
		expectedRootless = containers.RootlessModeEnabled
	}

	if backend.config.RootlessMode != expectedRootless {
		t.Errorf("Expected rootless mode %v, got %v", expectedRootless, backend.config.RootlessMode)
	}

	t.Logf("Auto-detected rootless mode: %v (current user: %s, uid: %s)",
		backend.config.RootlessMode, currentUser.Username, currentUser.Uid)
}

func TestContainerdBackend_detectRootlessMode_Explicit(t *testing.T) {
	// Test explicit rootless mode setting
	config := containers.ContainerdConfig{
		RootlessMode: containers.RootlessModeEnabled,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	err := backend.detectRootlessMode()
	if err != nil {
		t.Fatalf("detectRootlessMode failed: %v", err)
	}

	if backend.config.RootlessMode != containers.RootlessModeEnabled {
		t.Errorf("Expected explicit rootless mode to be preserved")
	}
}

func TestContainerdBackend_detectRootlessMode_ExplicitRoot(t *testing.T) {
	// Test explicit root mode setting
	config := containers.ContainerdConfig{
		RootlessMode: containers.RootlessModeDisabled,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	err := backend.detectRootlessMode()
	if err != nil {
		t.Fatalf("detectRootlessMode failed: %v", err)
	}

	if backend.config.RootlessMode != containers.RootlessModeDisabled {
		t.Errorf("Expected explicit root mode to be preserved")
	}
}

func TestContainerdBackend_detectRootlessMode_Conflict(t *testing.T) {
	// Test conflict: rootless mode enabled but running as root
	config := containers.ContainerdConfig{
		RootlessMode: containers.RootlessModeEnabled,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() failed: %v", err)
	}

	if currentUser.Uid == "0" {
		// We're running as root, so this should fail
		err := backend.detectRootlessMode()
		if err == nil {
			t.Error("Expected error when enabling rootless mode as root user")
		} else {
			t.Logf("Correctly detected conflict: %v", err)
		}
	} else {
		t.Skip("Not running as root, skipping conflict test")
	}
}

func TestContainerdBackend_getSocketPath(t *testing.T) {
	// Test default socket path in rootless mode
	config := containers.ContainerdConfig{
		RootlessMode: containers.RootlessModeEnabled,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	socketPath, err := backend.getSocketPath()
	assert.NoError(t, err, "getSocketPath should succeed")
	// Should be /run/user/{uid}/tau/containerd/containerd.sock
	if !strings.HasPrefix(socketPath, "/run/user/") {
		t.Errorf("Expected socket path to start with /run/user/, got %s", socketPath)
	}
	if !strings.Contains(socketPath, "/tau/containerd/containerd.sock") {
		t.Errorf("Expected socket path to contain /tau/containerd/containerd.sock, got %s", socketPath)
	}

	t.Logf("Rootless socket path: %s", socketPath)
}

func TestContainerdBackend_getSocketPath_Explicit(t *testing.T) {
	// Test explicit socket path
	explicitPath := "/tmp/test.sock"
	config := containers.ContainerdConfig{
		SocketPath: explicitPath,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	socketPath, err := backend.getSocketPath()
	assert.NoError(t, err, "getSocketPath should succeed")
	if socketPath != explicitPath {
		t.Errorf("Expected explicit socket path %s, got %s", explicitPath, socketPath)
	}
}

func TestContainerdBackend_getSocketPath_RootMode(t *testing.T) {
	// Test default socket path in root mode
	config := containers.ContainerdConfig{
		RootlessMode: containers.RootlessModeDisabled,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	socketPath, err := backend.getSocketPath()
	assert.NoError(t, err, "getSocketPath should succeed")
	expectedPath := "/run/containerd/containerd.sock"

	if socketPath != expectedPath {
		t.Errorf("Expected root socket path %s, got %s", expectedPath, socketPath)
	}
}

func TestContainerdBackend_isRootlessMode(t *testing.T) {
	tests := []struct {
		name         string
		rootlessMode containers.RootlessMode
		expected     bool
	}{
		{"auto mode", containers.RootlessModeAuto, false},
		{"disabled", containers.RootlessModeDisabled, false},
		{"enabled", containers.RootlessModeEnabled, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := containers.ContainerdConfig{
				RootlessMode: tt.rootlessMode,
			}

			backend := &ContainerdBackend{
				config: config,
			}

			result := backend.isRootlessMode()
			if result != tt.expected {
				t.Errorf("isRootlessMode() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestContainerdBackend_validateUIDGIDMapping(t *testing.T) {
	config := containers.ContainerdConfig{
		RootlessMode: containers.RootlessModeEnabled,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	// For rootless mode, UID/GID mapping validation should pass if subuid/subgid are configured
	// This is a critical requirement for rootless containers to work
	err := backend.validateUIDGIDMapping()
	assert.NoError(t, err, "UID/GID mapping validation should pass when subuid/subgid are configured for rootless mode")
}

func TestContainerdBackend_Capabilities(t *testing.T) {
	config := containers.ContainerdConfig{}

	backend := &ContainerdBackend{
		config: config,
	}

	caps := backend.Capabilities()

	// Check that all capabilities are set correctly
	expectedCaps := containers.BackendCapabilities{
		SupportsMemory:     true,
		SupportsCPU:        true,
		SupportsStorage:    true,
		SupportsPIDs:       true,
		SupportsMemorySwap: true,
		SupportsBuild:      true, // with BuildKit
		SupportsOCI:        true,
		SupportsNetworking: true,
		SupportsVolumes:    true,
	}

	if caps != expectedCaps {
		t.Errorf("Capabilities() = %+v, expected %+v", caps, expectedCaps)
	}
}

func TestContainerdBackend_TestSocketConnection(t *testing.T) {
	backend := &ContainerdBackend{
		config: containers.ContainerdConfig{
			RootlessMode: containers.RootlessModeEnabled,
			Namespace:    "test",
		},
	}

	socketPath, err := backend.getSocketPath()
	assert.NoError(t, err, "getSocketPath should succeed")

	// Ensure socket file doesn't exist
	os.Remove(socketPath)

	// Test with non-existent socket
	err = backend.TestSocketConnection()
	if err == nil {
		t.Fatalf("TestSocketConnection should return an error for non-existent socket at %s", socketPath)
	}
	assert.Contains(t, err.Error(), "does not exist")

	// Create a fake socket file
	// Ensure directory exists
	socketDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		t.Fatalf("Failed to create socket directory: %v", err)
	}
	socketFile, err := os.Create(socketPath)
	assert.NoError(t, err)
	socketFile.Close()

	// Should still fail since it's not a real socket
	err = backend.TestSocketConnection()
	assert.Error(t, err, "Socket connection should fail for fake file")
	assert.Contains(t, err.Error(), "failed to connect to socket")

	// Clean up
	os.Remove(socketPath)
}

func TestContainerdBackend_FullIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Check if containerd binary is available
	testDaemon := &Daemon{}
	_, err := testDaemon.findContainerdBinary()
	if err != nil {
		t.Skip("Skipping integration test: containerd binary not found")
	}

	// Try to create the backend - this should start containerd if needed
	// If this fails, it's because our code failed to configure/start containerd properly
	backend, err := NewContainerdBackend(containers.ContainerdConfig{
		RootlessMode: containers.RootlessModeAuto,
		AutoStart:    true,
		Namespace:    "tau-test",
	})

	// Backend creation should either succeed (if containerd starts) or fail with a clear error
	// If it fails due to missing rootless configuration, that's a real failure our code should handle
	if err != nil {
		t.Fatalf("Backend creation failed - our code should configure containerd for rootless mode: %v", err)
	}

	defer func() {
		// Clean up safely
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
		if backend != nil && backend.daemon != nil {
			backend.daemon.Stop(context.Background())
		}
	}()

	// If we got here, containerd is running and we have a client
	assert.NotNil(t, backend.client, "Client should be initialized")
	assert.NotNil(t, backend.daemon, "Daemon should be initialized")

	// Test that socket connection works
	err = backend.TestSocketConnection()
	assert.NoError(t, err, "Socket connection should work after successful init")

	// Test getting version through the client
	version, err := backend.client.Version(backend.client.ctx)
	assert.NoError(t, err, "Should be able to get containerd version")
	assert.NotEmpty(t, version.Version, "Version should not be empty")

	t.Logf("Successfully connected to containerd version: %s", version.Version)
}

func TestContainerdBackend_SimpleContainerOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Check if containerd binary is available
	testDaemon := &Daemon{}
	_, err := testDaemon.findContainerdBinary()
	if err != nil {
		t.Skip("Skipping integration test: containerd binary not found")
	}

	// Check if rootlesskit is available (required for rootless mode)
	_, err = testDaemon.findRootlesskitBinary()
	if err != nil {
		t.Skip("Skipping integration test: rootlesskit not found (required for rootless mode)")
	}

	// Use rootless mode with AutoStart - this should start containerd via rootlesskit
	backend, err := NewContainerdBackend(containers.ContainerdConfig{
		RootlessMode: containers.RootlessModeEnabled, // Use rootless mode
		AutoStart:    true,
		Namespace:    "tau-test",
	})

	// Backend creation should succeed if rootlesskit and containerd are available
	if err != nil {
		t.Fatalf("Backend creation failed - rootless containerd should start: %v", err)
	}

	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
		if backend != nil && backend.daemon != nil {
			backend.daemon.Stop(context.Background())
		}
	}()

	// Test 1: Create container that outputs "hello world"
	containerConfig := &containers.ContainerConfig{
		Image:   "quay.io/libpod/alpine:latest",
		Command: []string{"echo", "hello world"},
	}

	containerID, err := backend.Create(context.Background(), containerConfig)
	assert.NoError(t, err, "Container creation should succeed")
	assert.NotEmpty(t, containerID, "Container ID should not be empty")

	// Start the container
	err = backend.Start(context.Background(), containerID)
	assert.NoError(t, err, "Container start should succeed")

	// Wait for it to finish
	err = backend.Wait(context.Background(), containerID)
	assert.NoError(t, err, "Container should exit successfully")

	// Get logs to verify output
	logs, err := backend.Logs(context.Background(), containerID)
	assert.NoError(t, err, "Getting logs should succeed")
	assert.NotNil(t, logs, "Logs reader should not be nil")

	logData, err := io.ReadAll(logs)
	assert.NoError(t, err, "Reading logs should succeed")
	logs.Close()

	// Verify logs contain expected output
	assert.Contains(t, string(logData), "hello world", "Logs should contain 'hello world'")
	t.Logf("Container output: %q", string(logData))

	// Inspect to check exit code
	info, err := backend.Inspect(context.Background(), containerID)
	assert.NoError(t, err, "Container inspection should succeed")
	assert.Equal(t, 0, info.ExitCode, "Container should exit with code 0")

	// Clean up
	err = backend.Remove(context.Background(), containerID)
	assert.NoError(t, err, "Container removal should succeed")
}

func TestContainerdBackend_ContainerExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Check if containerd binary is available
	testDaemon := &Daemon{}
	_, err := testDaemon.findContainerdBinary()
	if err != nil {
		t.Skip("Skipping integration test: containerd binary not found")
	}

	// Check if rootlesskit is available (required for rootless mode)
	_, err = testDaemon.findRootlesskitBinary()
	if err != nil {
		t.Skip("Skipping integration test: rootlesskit not found (required for rootless mode)")
	}

	// Use rootless mode with AutoStart - this should start containerd via rootlesskit
	backend, err := NewContainerdBackend(containers.ContainerdConfig{
		RootlessMode: containers.RootlessModeEnabled, // Use rootless mode
		AutoStart:    true,
		Namespace:    "tau-test",
	})

	// Backend creation should succeed if rootlesskit and containerd are available
	if err != nil {
		t.Fatalf("Backend creation failed - rootless containerd should start: %v", err)
	}

	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
		if backend != nil && backend.daemon != nil {
			backend.daemon.Stop(context.Background())
		}
	}()

	// Test 2: Create container that exits with code 42
	containerConfig := &containers.ContainerConfig{
		Image:   "quay.io/libpod/alpine:latest",
		Command: []string{"sh", "-c", "exit 42"},
	}

	containerID, err := backend.Create(context.Background(), containerConfig)
	assert.NoError(t, err, "Container creation should succeed")

	// Start the container
	err = backend.Start(context.Background(), containerID)
	assert.NoError(t, err, "Container start should succeed")

	// Wait for it to finish - this should fail with exit code 42
	err = backend.Wait(context.Background(), containerID)
	assert.Error(t, err, "Container wait should fail with exit code 42")
	assert.Contains(t, err.Error(), "exited with status 42", "Error should contain exit status 42")

	// Inspect to check exit code
	info, err := backend.Inspect(context.Background(), containerID)
	assert.NoError(t, err, "Container inspection should succeed")
	assert.Equal(t, 42, info.ExitCode, "Container should exit with code 42")

	// Clean up
	err = backend.Remove(context.Background(), containerID)
	assert.NoError(t, err, "Container removal should succeed")
}

func TestContainerdBackend_ContainerOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Check if containerd binary is available
	testDaemon := &Daemon{}
	_, err := testDaemon.findContainerdBinary()
	if err != nil {
		t.Skip("Skipping integration test: containerd binary not found")
	}

	// Check if rootlesskit is available (required for rootless mode)
	_, err = testDaemon.findRootlesskitBinary()
	if err != nil {
		t.Skip("Skipping integration test: rootlesskit not found (required for rootless mode)")
	}

	// Use rootless mode with AutoStart
	backend, err := NewContainerdBackend(containers.ContainerdConfig{
		RootlessMode: containers.RootlessModeEnabled,
		AutoStart:    true,
		Namespace:    "tau-test",
	})

	if err != nil {
		t.Fatalf("Backend creation failed - rootless containerd should start: %v", err)
	}

	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
		if backend != nil && backend.daemon != nil {
			backend.daemon.Stop(context.Background())
		}
	}()

	t.Run("MultiLineOutput", func(t *testing.T) {
		// Test container that outputs multiple lines
		containerConfig := &containers.ContainerConfig{
			Image:   "quay.io/libpod/alpine:latest",
			Command: []string{"sh", "-c", "echo 'line 1'; echo 'line 2'; echo 'line 3'"},
		}

		containerID, err := backend.Create(context.Background(), containerConfig)
		assert.NoError(t, err, "Container creation should succeed")

		err = backend.Start(context.Background(), containerID)
		assert.NoError(t, err, "Container start should succeed")

		err = backend.Wait(context.Background(), containerID)
		assert.NoError(t, err, "Container should exit successfully")

		logs, err := backend.Logs(context.Background(), containerID)
		assert.NoError(t, err, "Getting logs should succeed")
		defer logs.Close()

		logData, err := io.ReadAll(logs)
		assert.NoError(t, err, "Reading logs should succeed")

		output := string(logData)
		assert.Contains(t, output, "line 1", "Logs should contain 'line 1'")
		assert.Contains(t, output, "line 2", "Logs should contain 'line 2'")
		assert.Contains(t, output, "line 3", "Logs should contain 'line 3'")
		t.Logf("Multi-line output: %q", output)

		backend.Remove(context.Background(), containerID)
	})

	t.Run("StdoutAndStderr", func(t *testing.T) {
		// Test container that outputs to both stdout and stderr
		containerConfig := &containers.ContainerConfig{
			Image:   "quay.io/libpod/alpine:latest",
			Command: []string{"sh", "-c", "echo 'stdout message' && echo 'stderr message' >&2"},
		}

		containerID, err := backend.Create(context.Background(), containerConfig)
		assert.NoError(t, err, "Container creation should succeed")

		err = backend.Start(context.Background(), containerID)
		assert.NoError(t, err, "Container start should succeed")

		err = backend.Wait(context.Background(), containerID)
		assert.NoError(t, err, "Container should exit successfully")

		logs, err := backend.Logs(context.Background(), containerID)
		assert.NoError(t, err, "Getting logs should succeed")
		defer logs.Close()

		logData, err := io.ReadAll(logs)
		assert.NoError(t, err, "Reading logs should succeed")

		output := string(logData)
		assert.Contains(t, output, "stdout message", "Logs should contain stdout output")
		assert.Contains(t, output, "stderr message", "Logs should contain stderr output")
		t.Logf("Combined stdout/stderr output: %q", output)

		backend.Remove(context.Background(), containerID)
	})

	t.Run("SpecialCharacters", func(t *testing.T) {
		// Test container output with special characters
		containerConfig := &containers.ContainerConfig{
			Image:   "quay.io/libpod/alpine:latest",
			Command: []string{"sh", "-c", "echo 'Hello, World! 123 @#$%^&*()'"},
		}

		containerID, err := backend.Create(context.Background(), containerConfig)
		assert.NoError(t, err, "Container creation should succeed")

		err = backend.Start(context.Background(), containerID)
		assert.NoError(t, err, "Container start should succeed")

		err = backend.Wait(context.Background(), containerID)
		assert.NoError(t, err, "Container should exit successfully")

		logs, err := backend.Logs(context.Background(), containerID)
		assert.NoError(t, err, "Getting logs should succeed")
		defer logs.Close()

		logData, err := io.ReadAll(logs)
		assert.NoError(t, err, "Reading logs should succeed")

		output := string(logData)
		assert.Contains(t, output, "Hello, World!", "Logs should contain the message")
		assert.Contains(t, output, "123", "Logs should contain numbers")
		assert.Contains(t, output, "@#$%^&*()", "Logs should contain special characters")
		t.Logf("Special characters output: %q", output)

		backend.Remove(context.Background(), containerID)
	})

	t.Run("LargeOutput", func(t *testing.T) {
		// Test container with larger output (multiple KB)
		containerConfig := &containers.ContainerConfig{
			Image:   "quay.io/libpod/alpine:latest",
			Command: []string{"sh", "-c", "for i in $(seq 1 100); do echo 'This is line number' $i; done"},
		}

		containerID, err := backend.Create(context.Background(), containerConfig)
		assert.NoError(t, err, "Container creation should succeed")

		err = backend.Start(context.Background(), containerID)
		assert.NoError(t, err, "Container start should succeed")

		err = backend.Wait(context.Background(), containerID)
		assert.NoError(t, err, "Container should exit successfully")

		logs, err := backend.Logs(context.Background(), containerID)
		assert.NoError(t, err, "Getting logs should succeed")
		defer logs.Close()

		logData, err := io.ReadAll(logs)
		assert.NoError(t, err, "Reading logs should succeed")

		output := string(logData)
		assert.Greater(t, len(output), 1000, "Output should be substantial (multiple KB)")
		assert.Contains(t, output, "line number 1", "Logs should contain first line")
		assert.Contains(t, output, "line number 100", "Logs should contain last line")
		t.Logf("Large output: %d bytes, first 200 chars: %q", len(output), output[:min(200, len(output))])

		backend.Remove(context.Background(), containerID)
	})

	t.Run("EmptyOutput", func(t *testing.T) {
		// Test container that produces no output
		containerConfig := &containers.ContainerConfig{
			Image:   "quay.io/libpod/alpine:latest",
			Command: []string{"true"}, // true produces no output
		}

		containerID, err := backend.Create(context.Background(), containerConfig)
		assert.NoError(t, err, "Container creation should succeed")

		err = backend.Start(context.Background(), containerID)
		assert.NoError(t, err, "Container start should succeed")

		err = backend.Wait(context.Background(), containerID)
		assert.NoError(t, err, "Container should exit successfully")

		logs, err := backend.Logs(context.Background(), containerID)
		assert.NoError(t, err, "Getting logs should succeed")
		defer logs.Close()

		logData, err := io.ReadAll(logs)
		assert.NoError(t, err, "Reading logs should succeed")

		// Empty output is valid - container ran successfully but produced no output
		t.Logf("Empty output (expected): %q", string(logData))

		backend.Remove(context.Background(), containerID)
	})
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
