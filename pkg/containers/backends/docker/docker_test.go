package docker

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

func waitForContainerStatus(t *testing.T, backend *DockerBackend, ctx context.Context, containerID core.ContainerID, expectedStatuses []string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		select {
		case <-ticker.C:
			info, err := backend.Inspect(ctx, containerID)
			if err != nil {
				continue
			}
			for _, expected := range expectedStatuses {
				if info.Status == expected {
					return info.Status
				}
			}
		case <-ctx.Done():
			t.Fatalf("Context cancelled while waiting for container status")
		}
	}

	info, err := backend.Inspect(ctx, containerID)
	require.NoError(t, err, "Final container inspect must succeed")
	return info.Status
}

func TestRegistration(t *testing.T) {
	// Test that we can create a Docker backend directly
	backend, err := New(core.DockerConfig{})
	require.NoError(t, err, "New must succeed for Docker")
	require.NotNil(t, backend, "Backend must not be nil")

	defer func() {
		if backend.client != nil {
			require.NoError(t, backend.client.Close(), "Client close must succeed")
		}
	}()

	// Verify it's a valid backend by checking capabilities
	caps := backend.Capabilities()
	assert.NotNil(t, caps, "Backend must have capabilities")
}

func TestFullIntegration(t *testing.T) {
	backend, err := New(core.DockerConfig{})
	require.NoError(t, err, "Backend creation must succeed - Docker is required")
	require.NotNil(t, backend, "Backend must not be nil")

	defer func() {
		require.NotNil(t, backend.client, "Client must exist for cleanup")
		require.NoError(t, backend.client.Close(), "Client close must succeed")
	}()

	ctx := context.Background()

	err = backend.HealthCheck(ctx)
	require.NoError(t, err, "HealthCheck must succeed - Docker daemon must be running")

	caps := backend.Capabilities()
	require.True(t, caps.SupportsBuild, "Docker must support building")
	require.True(t, caps.SupportsNetworking, "Docker must support networking")
	require.True(t, caps.SupportsMemory, "Docker must support memory limits")
	require.True(t, caps.SupportsCPU, "Docker must support CPU limits")
	require.True(t, caps.SupportsVolumes, "Docker must support volumes")

	image := backend.Image("alpine:latest")
	require.NotNil(t, image, "Image must not be nil")
	assert.Equal(t, "alpine:latest", image.Name(), "Image name must match")

	if !image.Exists(ctx) {
		err = image.Pull(ctx)
		require.NoError(t, err, "Image pull must succeed - Docker must be able to pull images")
		require.True(t, image.Exists(ctx), "Image must exist after pull")
	}

	config := &core.ContainerConfig{
		Image:   "alpine:latest",
		Command: []string{"echo", "test"},
	}

	containerID, err := backend.Create(ctx, config)
	require.NoError(t, err, "Container creation must succeed")
	require.NotEmpty(t, containerID, "Container ID must not be empty")

	defer func() {
		removeErr := backend.Remove(ctx, containerID)
		require.NoError(t, removeErr, "Container removal must succeed")
	}()

	info, err := backend.Inspect(ctx, containerID)
	require.NoError(t, err, "Container inspect must succeed")
	require.NotNil(t, info, "Container info must not be nil")
	assert.Equal(t, containerID, info.ID, "Container ID must match")
	assert.Equal(t, "alpine:latest", info.Image, "Container image must match")

	err = backend.Start(ctx, containerID)
	require.NoError(t, err, "Container start must succeed")

	validStatuses := []string{"running", "created", "exited"}
	status := waitForContainerStatus(t, backend, ctx, containerID, validStatuses, 5*time.Second)
	assert.Contains(t, validStatuses, status, "Container must be in a valid state after start: %s", status)

	if status == "running" {
		err = backend.Stop(ctx, containerID)
		require.NoError(t, err, "Container stop must succeed")

		stoppedStatuses := []string{"exited", "stopped", "dead"}
		finalStatus := waitForContainerStatus(t, backend, ctx, containerID, stoppedStatuses, 5*time.Second)
		assert.Contains(t, stoppedStatuses, finalStatus, "Container must be stopped after stop: %s", finalStatus)
	} else {
		assert.Equal(t, "exited", status, "Container must be in exited state if command completed")
	}

	err = backend.Wait(ctx, containerID)
	require.NoError(t, err, "Container wait must succeed")

	logs, err := backend.Logs(ctx, containerID)
	require.NoError(t, err, "Container logs must succeed")
	require.NotNil(t, logs, "Logs reader must not be nil")

	logData, err := io.ReadAll(logs)
	require.NoError(t, err, "Reading logs must succeed")
	require.NoError(t, logs.Close(), "Logs close must succeed")

	logContent := string(logData)
	assert.Contains(t, logContent, "test", "Logs must contain the echo output")
}

func TestContainerOutput(t *testing.T) {
	backend, err := New(core.DockerConfig{})
	require.NoError(t, err, "Backend creation must succeed - Docker is required")
	require.NotNil(t, backend, "Backend must not be nil")

	defer func() {
		require.NotNil(t, backend.client, "Client must exist for cleanup")
		require.NoError(t, backend.client.Close(), "Client close must succeed")
	}()

	ctx := context.Background()

	image := backend.Image("alpine:latest")
	require.NotNil(t, image, "Image must not be nil")
	if !image.Exists(ctx) {
		err = image.Pull(ctx)
		require.NoError(t, err, "Image pull must succeed")
	}

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
		{
			name:             "StdoutAndStderr",
			command:          []string{"sh", "-c", "echo 'stdout'; echo 'stderr' >&2"},
			expect:           "stdout",
			expectedExitCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containerConfig := &core.ContainerConfig{
				Image:   "alpine:latest",
				Command: tt.command,
			}

			containerID, err := backend.Create(ctx, containerConfig)
			require.NoError(t, err, "Container creation must succeed")
			require.NotEmpty(t, containerID, "Container ID must not be empty")

			defer func() {
				removeErr := backend.Remove(ctx, containerID)
				require.NoError(t, removeErr, "Container removal must succeed")
			}()

			err = backend.Start(ctx, containerID)
			require.NoError(t, err, "Container start must succeed")

			err = backend.Wait(ctx, containerID)
			if tt.expectedExitCode != 0 {
				require.Error(t, err, "Wait must return error for non-zero exit code")
			} else {
				require.NoError(t, err, "Container wait must succeed for zero exit code")
			}

			info, err := backend.Inspect(ctx, containerID)
			require.NoError(t, err, "Container inspect must succeed")
			assert.Equal(t, tt.expectedExitCode, info.ExitCode, "Container must exit with expected code")

			if tt.expect != "" {
				logs, err := backend.Logs(ctx, containerID)
				require.NoError(t, err, "Container logs must succeed")
				require.NotNil(t, logs, "Logs reader must not be nil")

				logData, err := io.ReadAll(logs)
				require.NoError(t, err, "Reading logs must succeed")
				require.NoError(t, logs.Close(), "Logs close must succeed")

				logContent := string(logData)
				assert.Contains(t, logContent, tt.expect, "Logs must contain expected output: %s", tt.expect)
			}
		})
	}
}
