//go:build linux

package containerd

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/taubyte/tau/pkg/containers/core"
)

func TestNewDaemon(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	daemon, err := NewDaemon(config)
	assert.NoError(t, err, "NewDaemon should succeed")
	assert.NotNil(t, daemon, "Daemon should not be nil")

	// Check that directories and paths are set up correctly
	// Should be /run/user/{uid}/tau/containerd/containerd.sock (or XDG_RUNTIME_DIR)
	assert.Contains(t, daemon.socketPath, "/tau/containerd/containerd.sock")
	assert.Contains(t, daemon.stateFile, "/tau/containerd/containerd.pid")

	// Check that socket directory exists
	socketDir := filepath.Dir(daemon.socketPath)
	assert.DirExists(t, socketDir, "Socket directory should exist")
}

func TestNewDaemon_RootfulMode(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
	}

	daemon, err := NewDaemon(config)
	assert.NoError(t, err, "NewDaemon should succeed in rootful mode")
	assert.NotNil(t, daemon, "Daemon should not be nil")

	// Check that rootful mode uses system paths
	assert.Equal(t, "/run/containerd/containerd.sock", daemon.socketPath, "Rootful mode should use system socket path")
	assert.Equal(t, "/run/containerd/containerd.pid", daemon.stateFile, "Rootful mode should use system PID file path")

	// Check that socket directory exists (or can be created)
	socketDir := filepath.Dir(daemon.socketPath)
	// Directory might not exist if containerd is not running, but we should be able to create it
	if _, err := os.Stat(socketDir); os.IsNotExist(err) {
		err := os.MkdirAll(socketDir, 0755)
		assert.NoError(t, err, "Should be able to create socket directory")
		defer os.RemoveAll(socketDir)
	}
}

func TestDaemon_findContainerdBinary(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	daemon, err := NewDaemon(config)
	assert.NoError(t, err)

	// Test with explicit path
	config.ContainerdPath = "/usr/bin/containerd"
	daemon.config.ContainerdPath = "/usr/bin/containerd"

	path, err := daemon.findContainerdBinary()
	if _, statErr := os.Stat("/usr/bin/containerd"); statErr == nil {
		assert.NoError(t, err, "Should find containerd at explicit path")
		assert.Equal(t, "/usr/bin/containerd", path)
	} else {
		t.Log("containerd not found at /usr/bin/containerd, testing PATH lookup")
	}

	// Test PATH lookup
	daemon.config.ContainerdPath = ""
	path, err = daemon.findContainerdBinary()

	// This will depend on whether containerd is in PATH
	if err != nil {
		assert.Contains(t, err.Error(), "not found in PATH")
		t.Log("containerd not found in PATH (expected on systems without containerd installed)")
	} else {
		t.Logf("Found containerd at: %s", path)
	}
}

func TestDaemon_createConfigFile(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	daemon, err := NewDaemon(config)
	assert.NoError(t, err)

	// Get XDG directories
	xdgDataHome := os.Getenv("XDG_DATA_HOME")
	if xdgDataHome == "" {
		home, _ := os.UserHomeDir()
		xdgDataHome = filepath.Join(home, ".local", "share")
	}

	xdgRuntimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntimeDir == "" {
		currentUser, _ := user.Current()
		uid, _ := strconv.Atoi(currentUser.Uid)
		xdgRuntimeDir = filepath.Join("/run", "user", strconv.Itoa(uid))
	}

	rootDir := filepath.Join(xdgDataHome, "tau", "containerd", "daemon")
	stateDir := filepath.Join(xdgRuntimeDir, "tau", "containerd", "daemon")
	socketPath := "/run/containerd/containerd.sock"
	debugSocketPath := "/run/containerd/containerd-debug.sock"
	configDir := filepath.Join(xdgRuntimeDir, "tau", "containerd")

	configPath, err := daemon.createConfigFile(rootDir, stateDir, socketPath, debugSocketPath, configDir)
	assert.NoError(t, err, "createConfigFile should succeed")
	assert.NotEmpty(t, configPath, "Config path should not be empty")

	// Check that config file exists and contains expected content
	assert.FileExists(t, configPath, "Config file should exist")

	content, err := os.ReadFile(configPath)
	assert.NoError(t, err)
	contentStr := string(content)

	// Check for Docker-style minimal config (CRI disabled)
	assert.Contains(t, contentStr, "disabled_plugins = [\"io.containerd.grpc.v1.cri\"]", "Config should disable CRI plugin")
	assert.Contains(t, contentStr, "version = 2", "Config should have version 2")
	assert.Contains(t, contentStr, "[grpc]", "Config should have grpc section")
	assert.Contains(t, contentStr, socketPath, "Config should contain socket path")

	// Clean up
	os.Remove(configPath)
}

func TestDaemon_isRunning(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	daemon, err := NewDaemon(config)
	assert.NoError(t, err)

	// Initially should not be running
	assert.False(t, daemon.isRunning(), "Daemon should not be running initially")

	// Create a fake PID file to test detection
	pidData := "999999"
	err = os.WriteFile(daemon.stateFile, []byte(pidData), 0644)
	assert.NoError(t, err)

	// Should still return false since PID doesn't exist
	assert.False(t, daemon.isRunning(), "Daemon should not be running with fake PID")

	// Clean up
	os.Remove(daemon.stateFile)
}

func TestDaemon_waitForSocket(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	daemon, err := NewDaemon(config)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Test with non-existent socket (should timeout)
	err = daemon.waitForSocket(ctx, 50*time.Millisecond)
	assert.Error(t, err, "waitForSocket should timeout for non-existent socket")
	assert.Contains(t, err.Error(), "context deadline exceeded")

	// Note: We can't easily test the success case without actually starting containerd
	// The integration test will cover this scenario
}

func TestDaemon_HealthCheck(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	daemon, err := NewDaemon(config)
	assert.NoError(t, err)

	// Health check should fail when daemon is not running
	err = daemon.HealthCheck(context.Background())
	assert.Error(t, err, "Health check should fail when daemon is not running")
	assert.Contains(t, err.Error(), "not running")
}

func TestDaemon_FullIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Check if rootlesskit is available
	testDaemon := &Daemon{}
	_, err := testDaemon.findRootlesskitBinary()
	if err != nil {
		t.Skip("Skipping integration test: rootlesskit not found")
	}

	// Check if containerd binary is available
	_, err = testDaemon.findContainerdBinary()
	if err != nil {
		t.Skip("Skipping integration test: containerd binary not found")
	}

	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
		AutoStart:    true,
		Namespace:    "test",
	}

	daemon, err := NewDaemon(config)
	assert.NoError(t, err)

	// Clean up any existing state
	os.Remove(daemon.socketPath)
	os.Remove(daemon.stateFile)

	ctx := context.Background()

	// Test starting daemon
	err = daemon.Start(ctx)
	assert.NoError(t, err, "Daemon should start successfully when containerd and rootlesskit are available. If this fails, check subuid/subgid mappings and network driver availability")

	defer func() {
		// Clean up
		daemon.Stop(ctx)
		os.Remove(daemon.socketPath)
		os.Remove(daemon.stateFile)
	}()

	// Should be running now
	assert.True(t, daemon.isRunning(), "Daemon should be running after start")

	// Socket should be ready
	assert.True(t, daemon.isSocketReady(), "Socket should be ready")

	// Health check should pass
	err = daemon.HealthCheck(ctx)
	assert.NoError(t, err, "Health check should pass")

	// Test stopping daemon
	err = daemon.Stop(ctx)
	assert.NoError(t, err, "Stop should succeed")

	// Should not be running anymore
	assert.False(t, daemon.isRunning(), "Daemon should not be running after stop")
}

func TestDaemon_connectToSocket(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	daemon, err := NewDaemon(config)
	assert.NoError(t, err)

	// Test connecting to non-existent socket
	conn, err := daemon.connectToSocket()
	assert.Error(t, err, "Connecting to non-existent socket should fail")
	assert.Nil(t, conn, "Connection should be nil")

	// Create a fake socket file (but not a real socket)
	socketFile, err := os.Create(daemon.socketPath)
	assert.NoError(t, err)
	socketFile.Close()

	// Should still fail since it's not a real socket
	conn, err = daemon.connectToSocket()
	assert.Error(t, err, "Connecting to fake file should fail")
	assert.Nil(t, conn, "Connection should be nil")

	// Clean up
	os.Remove(daemon.socketPath)
}
