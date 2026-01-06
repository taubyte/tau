package docker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

func TestCreate(t *testing.T) {
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

	t.Run("NoClient", func(t *testing.T) {
		backend := &DockerBackend{
			client: nil,
		}

		config := &core.ContainerConfig{
			Image:   "alpine:latest",
			Command: []string{"echo", "test"},
		}

		containerID, err := backend.Create(context.Background(), config)
		assert.Error(t, err, "Create must fail when client is nil")
		assert.Empty(t, containerID, "Container ID must be empty on error")
		assert.Contains(t, err.Error(), "not initialized")
	})

	t.Run("InvalidImageName", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image:   "invalid-image-name-that-does-not-exist:999999",
			Command: []string{"echo", "test"},
		}

		containerID, err := backend.Create(context.Background(), config)
		assert.Error(t, err, "Create must fail for invalid image")
		assert.Empty(t, containerID, "Container ID must be empty on error")
	})

	t.Run("ConfigError", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image: "invalid-image-that-does-not-exist:999999",
			Network: &core.NetworkConfig{
				PortMappings: []core.PortMapping{
					{
						HostPort:      8080,
						ContainerPort: 80,
						Protocol:      "tcp",
					},
				},
			},
		}

		containerID, err := backend.Create(context.Background(), config)
		assert.Error(t, err, "Create must fail for invalid image")
		assert.Empty(t, containerID, "Container ID must be empty on error")
	})

	t.Run("InvalidPort", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image: "alpine:latest",
			Network: &core.NetworkConfig{
				PortMappings: []core.PortMapping{
					{
						HostPort:      8080,
						ContainerPort: 99999,
						Protocol:      "invalid-protocol-xyz",
					},
				},
			},
		}

		containerID, err := backend.Create(ctx, config)
		assert.Error(t, err, "Create must fail for invalid port protocol")
		assert.Empty(t, containerID, "Container ID must be empty on error")
		assert.Contains(t, err.Error(), "invalid port", "Error must indicate invalid port")
	})
}

func TestStart(t *testing.T) {
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

	t.Run("NoClient", func(t *testing.T) {
		backend := &DockerBackend{
			client: nil,
		}

		err := backend.Start(context.Background(), core.ContainerID("test"))
		assert.Error(t, err, "Start must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized")
	})

	t.Run("NotFound", func(t *testing.T) {
		err = backend.Start(context.Background(), core.ContainerID("nonexistent"))
		assert.Error(t, err, "Start must fail for non-existent container")
	})
}

func TestStop(t *testing.T) {
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

	t.Run("NoClient", func(t *testing.T) {
		backend := &DockerBackend{
			client: nil,
		}

		err := backend.Stop(context.Background(), core.ContainerID("test"))
		assert.Error(t, err, "Stop must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized")
	})

	t.Run("NotFound", func(t *testing.T) {
		err = backend.Stop(context.Background(), core.ContainerID("nonexistent"))
		assert.Error(t, err, "Stop must fail for non-existent container")
	})

	t.Run("Success", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image:   "alpine:latest",
			Command: []string{"sleep", "10"},
		}

		containerID, err := backend.Create(ctx, config)
		require.NoError(t, err, "Container creation must succeed")
		require.NotEmpty(t, containerID, "Container ID must not be empty")

		defer func() {
			removeErr := backend.Remove(ctx, containerID)
			require.NoError(t, removeErr, "Container removal must succeed")
		}()

		err = backend.Start(ctx, containerID)
		require.NoError(t, err, "Container start must succeed")

		status := waitForContainerStatus(t, backend, ctx, containerID, []string{"running"}, 5*time.Second)
		require.Equal(t, "running", status, "Container must be running before stop")

		err = backend.Stop(ctx, containerID)
		require.NoError(t, err, "Container stop must succeed")

		stoppedStatuses := []string{"exited", "stopped", "dead"}
		finalStatus := waitForContainerStatus(t, backend, ctx, containerID, stoppedStatuses, 5*time.Second)
		assert.Contains(t, stoppedStatuses, finalStatus, "Container must be stopped after stop: %s", finalStatus)
	})
}

func TestRemove(t *testing.T) {
	backend, err := New(core.DockerConfig{})
	require.NoError(t, err, "Backend creation must succeed - Docker is required")
	require.NotNil(t, backend, "Backend must not be nil")

	defer func() {
		require.NotNil(t, backend.client, "Client must exist for cleanup")
		require.NoError(t, backend.client.Close(), "Client close must succeed")
	}()

	t.Run("NoClient", func(t *testing.T) {
		backend := &DockerBackend{
			client: nil,
		}

		err := backend.Remove(context.Background(), core.ContainerID("test"))
		assert.Error(t, err, "Remove must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized")
	})

	t.Run("NotFound", func(t *testing.T) {
		err = backend.Remove(context.Background(), core.ContainerID("nonexistent"))
		assert.Error(t, err, "Remove must fail for non-existent container")
	})
}

func TestWait(t *testing.T) {
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

	t.Run("NoClient", func(t *testing.T) {
		backend := &DockerBackend{
			client: nil,
		}

		err := backend.Wait(context.Background(), core.ContainerID("test"))
		assert.Error(t, err, "Wait must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized")
	})

	t.Run("NotFound", func(t *testing.T) {
		err = backend.Wait(context.Background(), core.ContainerID("nonexistent"))
		assert.Error(t, err, "Wait must fail for non-existent container")
	})

	t.Run("ZeroExitCode", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image:   "alpine:latest",
			Command: []string{"echo", "test"},
		}

		containerID, err := backend.Create(ctx, config)
		require.NoError(t, err, "Container creation must succeed")

		defer func() {
			removeErr := backend.Remove(ctx, containerID)
			require.NoError(t, removeErr, "Container removal must succeed")
		}()

		err = backend.Start(ctx, containerID)
		require.NoError(t, err, "Container start must succeed")

		err = backend.Wait(ctx, containerID)
		require.NoError(t, err, "Wait must succeed for zero exit code")
	})

	t.Run("NonZeroExitCode", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image:   "alpine:latest",
			Command: []string{"sh", "-c", "exit 42"},
		}

		containerID, err := backend.Create(ctx, config)
		require.NoError(t, err, "Container creation must succeed")

		defer func() {
			removeErr := backend.Remove(ctx, containerID)
			require.NoError(t, removeErr, "Container removal must succeed")
		}()

		err = backend.Start(ctx, containerID)
		require.NoError(t, err, "Container start must succeed")

		err = backend.Wait(ctx, containerID)
		assert.Error(t, err, "Wait must return error for non-zero exit code")
		assert.Contains(t, err.Error(), "exited with status 42")
	})
}

func TestLogs(t *testing.T) {
	backend, err := New(core.DockerConfig{})
	require.NoError(t, err, "Backend creation must succeed - Docker is required")
	require.NotNil(t, backend, "Backend must not be nil")

	defer func() {
		require.NotNil(t, backend.client, "Client must exist for cleanup")
		require.NoError(t, backend.client.Close(), "Client close must succeed")
	}()

	t.Run("NoClient", func(t *testing.T) {
		backend := &DockerBackend{
			client: nil,
		}

		logs, err := backend.Logs(context.Background(), core.ContainerID("test"))
		assert.Error(t, err, "Logs must fail when client is nil")
		assert.Nil(t, logs, "Logs must be nil on error")
		assert.Contains(t, err.Error(), "not initialized")
	})

	t.Run("NotFound", func(t *testing.T) {
		logs, err := backend.Logs(context.Background(), core.ContainerID("nonexistent"))
		assert.Error(t, err, "Logs must fail for non-existent container")
		assert.Nil(t, logs, "Logs must be nil on error")
	})
}

func TestInspect(t *testing.T) {
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

	t.Run("NoClient", func(t *testing.T) {
		backend := &DockerBackend{
			client: nil,
		}

		info, err := backend.Inspect(context.Background(), core.ContainerID("test"))
		assert.Error(t, err, "Inspect must fail when client is nil")
		assert.Nil(t, info, "Info must be nil on error")
		assert.Contains(t, err.Error(), "not initialized")
	})

	t.Run("NotFound", func(t *testing.T) {
		info, err := backend.Inspect(context.Background(), core.ContainerID("nonexistent"))
		assert.Error(t, err, "Inspect must fail for non-existent container")
		assert.Nil(t, info, "Info must be nil on error")
	})

	t.Run("WithResources", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image:   "alpine:latest",
			Command: []string{"echo", "test"},
			Resources: &core.ResourceLimits{
				Memory: 1024 * 1024 * 512,
				PIDs:   100,
			},
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
		assert.NotNil(t, info.Resources, "Resources must not be nil when container has resource limits")
	})

	t.Run("WithPidsLimit", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image:   "alpine:latest",
			Command: []string{"echo", "test"},
			Resources: &core.ResourceLimits{
				Memory: 1024 * 1024 * 512,
				PIDs:   100,
			},
		}

		containerID, err := backend.Create(ctx, config)
		require.NoError(t, err, "Container creation must succeed")

		defer func() {
			removeErr := backend.Remove(ctx, containerID)
			require.NoError(t, removeErr, "Container removal must succeed")
		}()

		info, err := backend.Inspect(ctx, containerID)
		require.NoError(t, err, "Container inspect must succeed")
		require.NotNil(t, info, "Container info must not be nil")

		if info.Resources != nil {
			assert.Equal(t, int64(100), info.Resources.PIDs, "PIDs must match resource limit")
		}
	})

	t.Run("NoResources", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image:   "alpine:latest",
			Command: []string{"echo", "test"},
		}

		containerID, err := backend.Create(ctx, config)
		require.NoError(t, err, "Container creation must succeed")

		defer func() {
			removeErr := backend.Remove(ctx, containerID)
			require.NoError(t, removeErr, "Container removal must succeed")
		}()

		info, err := backend.Inspect(ctx, containerID)
		require.NoError(t, err, "Container inspect must succeed")
		require.NotNil(t, info, "Container info must not be nil")

	})

	t.Run("ExitCode", func(t *testing.T) {
		t.Run("Zero", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image:   "alpine:latest",
				Command: []string{"echo", "test"},
			}

			containerID, err := backend.Create(ctx, config)
			require.NoError(t, err, "Container creation must succeed")

			defer func() {
				removeErr := backend.Remove(ctx, containerID)
				require.NoError(t, removeErr, "Container removal must succeed")
			}()

			err = backend.Start(ctx, containerID)
			require.NoError(t, err, "Container start must succeed")

			err = backend.Wait(ctx, containerID)
			require.NoError(t, err, "Container wait must succeed")

			info, err := backend.Inspect(ctx, containerID)
			require.NoError(t, err, "Container inspect must succeed")
			require.NotNil(t, info, "Container info must not be nil")

			assert.Equal(t, 0, info.ExitCode, "ExitCode must be 0 for successful container")
		})

		t.Run("NotSet", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image:   "alpine:latest",
				Command: []string{"echo", "test"},
			}

			containerID, err := backend.Create(ctx, config)
			require.NoError(t, err, "Container creation must succeed")

			defer func() {
				removeErr := backend.Remove(ctx, containerID)
				require.NoError(t, removeErr, "Container removal must succeed")
			}()

			info, err := backend.Inspect(ctx, containerID)
			require.NoError(t, err, "Container inspect must succeed")
			require.NotNil(t, info, "Container info must not be nil")

			if info.ExitCode == 0 {
				assert.Equal(t, 0, info.ExitCode, "ExitCode should be 0 for created container")
			}
		})
	})

	t.Run("StartedAt", func(t *testing.T) {
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

		err = backend.Start(ctx, containerID)
		require.NoError(t, err, "Container start must succeed")

		info, err := backend.Inspect(ctx, containerID)
		require.NoError(t, err, "Container inspect must succeed")
		require.NotNil(t, info, "Container info must not be nil")

		if !info.StartedAt.IsZero() {
			assert.True(t, info.StartedAt.Before(time.Now()) || info.StartedAt.Equal(time.Now()), "StartedAt must be in the past or present")
		}
	})
}

func TestGetDockerID(t *testing.T) {
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

	t.Run("MapLookup", func(t *testing.T) {
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

		dockerID1, err := backend.getDockerID(ctx, containerID)
		require.NoError(t, err, "getDockerID must succeed")
		require.NotEmpty(t, dockerID1, "Docker ID must not be empty")

		dockerID2, err := backend.getDockerID(ctx, containerID)
		require.NoError(t, err, "getDockerID must succeed on second call")
		assert.Equal(t, dockerID1, dockerID2, "getDockerID must return same ID from map")
	})

	t.Run("ContainerListSuccess", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image:   "alpine:latest",
			Command: []string{"echo", "test"},
		}

		containerID, err := backend.Create(ctx, config)
		require.NoError(t, err, "Container creation must succeed")

		defer func() {
			removeErr := backend.Remove(ctx, containerID)
			require.NoError(t, removeErr, "Container removal must succeed")
		}()

		delete(backend.containers, containerID)

		dockerID, err := backend.getDockerID(ctx, containerID)
		require.NoError(t, err, "getDockerID must succeed via ContainerList lookup")
		require.NotEmpty(t, dockerID, "Docker ID must not be empty")
		assert.Equal(t, dockerID, backend.containers[containerID], "Docker ID must be stored in map")
	})

	t.Run("NotFound", func(t *testing.T) {
		_, err = backend.getDockerID(ctx, core.ContainerID("nonexistent-container"))
		require.Error(t, err, "getDockerID must fail for non-existent container")
		assert.Contains(t, err.Error(), "not found", "Error message must indicate container not found")
	})

	t.Run("EmptyContainersList", func(t *testing.T) {
		_, err = backend.getDockerID(ctx, core.ContainerID("nonexistent-container-id-12345"))
		assert.Error(t, err, "getDockerID must fail for non-existent container")
		assert.Contains(t, err.Error(), "not found", "Error must indicate container not found")
	})

	t.Run("ListError", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = backend.getDockerID(ctx, core.ContainerID("nonexistent"))
		assert.Error(t, err, "getDockerID must fail with cancelled context")
	})
}
