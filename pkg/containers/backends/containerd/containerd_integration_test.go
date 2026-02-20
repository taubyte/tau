//go:build linux && containerd_integration

package containerd

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

func TestContainerdBackend_FullIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	testDaemon := &Daemon{}
	_, err := testDaemon.findContainerdBinary()
	require.NoError(t, err, "Containerd binary must be available for this test")

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeAuto,
		AutoStart:    true,
		Namespace:    "tau-test",
	})
	require.NoError(t, err, "Backend creation must succeed (rootless environment with containerd and rootlesskit required): %v", err)

	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
		if backend != nil && backend.daemon != nil {
			backend.daemon.Stop(context.Background())
		}
	}()

	assert.NotNil(t, backend.client, "Client should be initialized")
	assert.NotNil(t, backend.daemon, "Daemon should be initialized")

	err = backend.testSocketConnection()
	assert.NoError(t, err, "Socket connection should work after successful init")

	version, err := backend.client.Version(backend.client.ctx)
	assert.NoError(t, err, "Should be able to get containerd version")
	assert.NotEmpty(t, version.Version, "Version should not be empty")

	t.Logf("Successfully connected to containerd version: %s", version.Version)
}

func TestContainerdBackend_SimpleContainerOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	testDaemon := &Daemon{}
	_, err := testDaemon.findContainerdBinary()
	require.NoError(t, err, "Containerd binary must be available for this test")
	_, err = testDaemon.findRootlesskitBinary()
	require.NoError(t, err, "Rootlesskit must be available for this test")

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
		AutoStart:    true,
		Namespace:    "tau-test",
	})
	require.NoError(t, err, "Backend creation must succeed: %v", err)

	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
		if backend != nil && backend.daemon != nil {
			backend.daemon.Stop(context.Background())
		}
	}()

	containerConfig := &core.ContainerConfig{
		Image:   "quay.io/libpod/alpine:latest",
		Command: []string{"echo", "hello world"},
	}

	containerID, err := backend.Create(context.Background(), containerConfig)
	assert.NoError(t, err, "Container creation should succeed")
	assert.NotEmpty(t, containerID, "Container ID should not be empty")

	err = backend.Start(context.Background(), containerID)
	assert.NoError(t, err, "Container start should succeed")

	err = backend.Wait(context.Background(), containerID)
	assert.NoError(t, err, "Container should exit successfully")

	logs, err := backend.Logs(context.Background(), containerID)
	assert.NoError(t, err, "Getting logs should succeed")
	assert.NotNil(t, logs, "Logs reader should not be nil")

	logData, err := io.ReadAll(logs)
	assert.NoError(t, err, "Reading logs should succeed")
	logs.Close()

	assert.Contains(t, string(logData), "hello world", "Logs should contain 'hello world'")
	t.Logf("Container output: %q", string(logData))

	info, err := backend.Inspect(context.Background(), containerID)
	assert.NoError(t, err, "Container inspection should succeed")
	assert.Equal(t, 0, info.ExitCode, "Container should exit with code 0")

	err = backend.Remove(context.Background(), containerID)
	assert.NoError(t, err, "Container removal should succeed")
}

func TestContainerdBackend_ContainerExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	testDaemon := &Daemon{}
	_, err := testDaemon.findContainerdBinary()
	require.NoError(t, err, "Containerd binary must be available for this test")
	_, err = testDaemon.findRootlesskitBinary()
	require.NoError(t, err, "Rootlesskit must be available for this test")

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
		AutoStart:    true,
		Namespace:    "tau-test",
	})
	require.NoError(t, err, "Backend creation must succeed: %v", err)

	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
		if backend != nil && backend.daemon != nil {
			backend.daemon.Stop(context.Background())
		}
	}()

	containerConfig := &core.ContainerConfig{
		Image:   "quay.io/libpod/alpine:latest",
		Command: []string{"sh", "-c", "exit 42"},
	}

	containerID, err := backend.Create(context.Background(), containerConfig)
	assert.NoError(t, err, "Container creation should succeed")

	err = backend.Start(context.Background(), containerID)
	assert.NoError(t, err, "Container start should succeed")

	err = backend.Wait(context.Background(), containerID)
	assert.Error(t, err, "Container wait should fail with exit code 42")
	assert.Contains(t, err.Error(), "exited with status 42", "Error should contain exit status 42")

	info, err := backend.Inspect(context.Background(), containerID)
	assert.NoError(t, err, "Container inspection should succeed")
	assert.Equal(t, 42, info.ExitCode, "Container should exit with code 42")

	err = backend.Remove(context.Background(), containerID)
	assert.NoError(t, err, "Container removal should succeed")
}

func TestContainerdBackend_ContainerOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	testDaemon := &Daemon{}
	_, err := testDaemon.findContainerdBinary()
	require.NoError(t, err, "Containerd binary must be available for this test")
	_, err = testDaemon.findRootlesskitBinary()
	require.NoError(t, err, "Rootlesskit must be available for this test")

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
		AutoStart:    true,
		Namespace:    "tau-test",
	})
	require.NoError(t, err, "Backend creation must succeed: %v", err)

	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
		if backend != nil && backend.daemon != nil {
			backend.daemon.Stop(context.Background())
		}
	}()

	t.Run("MultiLineOutput", func(t *testing.T) {
		containerConfig := &core.ContainerConfig{
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
		containerConfig := &core.ContainerConfig{
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
		containerConfig := &core.ContainerConfig{
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
		containerConfig := &core.ContainerConfig{
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
		n := 200
		if len(output) < n {
			n = len(output)
		}
		t.Logf("Large output: %d bytes, first 200 chars: %q", len(output), output[:n])

		backend.Remove(context.Background(), containerID)
	})

	t.Run("EmptyOutput", func(t *testing.T) {
		containerConfig := &core.ContainerConfig{
			Image:   "quay.io/libpod/alpine:latest",
			Command: []string{"true"},
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

		t.Logf("Empty output (expected): %q", string(logData))

		backend.Remove(context.Background(), containerID)
	})
}

func TestContainerdBackend_RootfulMode_BackendCreation_WithSystemContainerd(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	socketPath := "/run/containerd/containerd.sock"
	conn, err := net.Dial("unix", socketPath)
	skipIfSystemContainerdUnavailable(t, socketPath, err)
	conn.Close()

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		Namespace:    "tau-test-rootful",
	})

	if err != nil {
		t.Fatalf("Backend creation should succeed when system containerd is running: %v", err)
	}

	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

	assert.NotNil(t, backend.client, "Client should be initialized")
	assert.Nil(t, backend.daemon, "Daemon should not be initialized in rootful mode (systemd manages it)")

	err = backend.testSocketConnection()
	assert.NoError(t, err, "Socket connection should work with system containerd")

	version, err := backend.client.Version(backend.client.ctx)
	assert.NoError(t, err, "Should be able to get containerd version")
	assert.NotEmpty(t, version.Version, "Version should not be empty")

	t.Logf("Successfully connected to system containerd version: %s", version.Version)
}

func TestContainerdBackend_RootfulMode_ContainerOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	socketPath := "/run/containerd/containerd.sock"
	conn, err := net.Dial("unix", socketPath)
	skipIfSystemContainerdUnavailable(t, socketPath, err)
	conn.Close()

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		Namespace:    "tau-test-rootful",
	})

	if err != nil {
		t.Fatalf("Backend creation failed: %v", err)
	}

	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

	containerConfig := &core.ContainerConfig{
		Image:   "quay.io/libpod/alpine:latest",
		Command: []string{"echo", "hello from rootful mode"},
	}

	containerID, err := backend.Create(context.Background(), containerConfig)
	assert.NoError(t, err, "Container creation should succeed")
	assert.NotEmpty(t, containerID, "Container ID should not be empty")

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
	assert.Contains(t, output, "hello from rootful mode", "Logs should contain the expected output")
	t.Logf("Rootful mode container output: %q", output)

	info, err := backend.Inspect(context.Background(), containerID)
	assert.NoError(t, err, "Container inspection should succeed")
	assert.Equal(t, 0, info.ExitCode, "Container should exit with code 0")

	err = backend.Remove(context.Background(), containerID)
	assert.NoError(t, err, "Container removal should succeed")
}

func TestContainerdBackend_NestedDocker_RootfulMode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tc, cleanup := setupContainerdInDocker(t)
	defer cleanup()

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		SocketPath:   tc.socketPath,
		Namespace:    "tau-test-reproducible",
	})

	require.NoError(t, err, "Backend creation should succeed")
	assert.NotNil(t, backend.client, "Client should be initialized")
	assert.Nil(t, backend.daemon, "Daemon should not be initialized (using Docker container)")

	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

	err = backend.testSocketConnection()
	assert.NoError(t, err, "Socket connection should work")

	version, err := backend.client.Version(backend.client.ctx)
	assert.NoError(t, err, "Should be able to get containerd version")
	assert.NotEmpty(t, version.Version, "Version should not be empty")
	t.Logf("Connected to containerd version: %s", version.Version)

	containerConfig := &core.ContainerConfig{
		Image:   "quay.io/libpod/alpine:latest",
		Command: []string{"echo", "hello from reproducible test"},
	}

	containerID, err := backend.Create(context.Background(), containerConfig)
	assert.NoError(t, err, "Container creation should succeed")
	assert.NotEmpty(t, containerID, "Container ID should not be empty")

	t.Logf("Containerd backend setup successful - basic containerd integration verified")

	_, err = backend.Inspect(context.Background(), containerID)
	assert.NoError(t, err, "Container inspection should succeed")

	err = backend.Remove(context.Background(), containerID)
	assert.NoError(t, err, "Container removal should succeed")
}

func TestContainerdBackend_NestedDocker_ContainerOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tc, cleanup := setupContainerdInDocker(t)
	defer cleanup()

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		SocketPath:   tc.socketPath,
		Namespace:    "tau-test-reproducible-ops",
	})

	require.NoError(t, err)
	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

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
			if tt.expectedExitCode != 0 {
				// Non-zero exit is expected
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

			info, err := backend.Inspect(context.Background(), containerID)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedExitCode, info.ExitCode, "Container should exit with expected code")

			err = backend.Remove(context.Background(), containerID)
			require.NoError(t, err)
		})
	}
}
