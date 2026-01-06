package docker

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

func TestCapabilities(t *testing.T) {
	backend := &DockerBackend{
		config: core.DockerConfig{},
	}

	caps := backend.Capabilities()

	expectedCaps := core.BackendCapabilities{
		SupportsMemory:     true,
		SupportsCPU:        true,
		SupportsStorage:    true,
		SupportsPIDs:       true,
		SupportsMemorySwap: true,
		SupportsBuild:      true,
		SupportsOCI:        true,
		SupportsNetworking: true,
		SupportsVolumes:    true,
	}

	assert.Equal(t, expectedCaps, caps, "Capabilities should match expected values")
}

func TestInitClient(t *testing.T) {
	t.Run("DefaultHost", func(t *testing.T) {
		backend := &DockerBackend{
			config: core.DockerConfig{},
		}

		originalHost := os.Getenv("DOCKER_HOST")
		defer func() {
			if originalHost != "" {
				require.NoError(t, os.Setenv("DOCKER_HOST", originalHost))
			} else {
				require.NoError(t, os.Unsetenv("DOCKER_HOST"))
			}
		}()

		require.NoError(t, os.Unsetenv("DOCKER_HOST"))

		err := backend.initClient()
		require.NoError(t, err, "initClient must succeed")
		assert.NotNil(t, backend.client, "Client must be initialized")
	})

	t.Run("CustomHost", func(t *testing.T) {
		backend := &DockerBackend{
			config: core.DockerConfig{
				Host: "unix:///var/run/docker.sock",
			},
		}

		err := backend.initClient()
		require.NoError(t, err, "initClient must succeed")
		assert.NotNil(t, backend.client, "Client must be initialized")
		assert.Equal(t, "unix:///var/run/docker.sock", backend.config.Host, "Host must be set correctly")
	})

	t.Run("EnvHost", func(t *testing.T) {
		backend := &DockerBackend{
			config: core.DockerConfig{},
		}

		originalHost := os.Getenv("DOCKER_HOST")
		defer func() {
			if originalHost != "" {
				require.NoError(t, os.Setenv("DOCKER_HOST", originalHost))
			} else {
				require.NoError(t, os.Unsetenv("DOCKER_HOST"))
			}
		}()

		require.NoError(t, os.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock"))

		err := backend.initClient()
		require.NoError(t, err, "initClient must succeed")
		assert.NotNil(t, backend.client, "Client must be initialized")
	})

	t.Run("APIVersion", func(t *testing.T) {
		backend := &DockerBackend{
			config: core.DockerConfig{
				APIVersion: "1.40",
			},
		}

		err := backend.initClient()
		require.NoError(t, err, "initClient must succeed with APIVersion")
		assert.NotNil(t, backend.client, "Client must be initialized")
	})

	t.Run("InitClientFailure", func(t *testing.T) {
		backend := &DockerBackend{
			config: core.DockerConfig{
				Host: "invalid://host:9999",
			},
			containers: make(map[core.ContainerID]string),
		}

		err := backend.initClient()
		if err != nil {
			assert.Contains(t, err.Error(), "failed to create Docker client", "initClient must fail with invalid host")
		}
	})
}

func TestNew(t *testing.T) {
	t.Run("HealthCheckError", func(t *testing.T) {
		backend := &DockerBackend{
			config: core.DockerConfig{
				Host: "tcp://127.0.0.1:1",
			},
			containers: make(map[core.ContainerID]string),
		}

		err := backend.initClient()
		if err == nil {
			_, err = New(backend.config)
			assert.Error(t, err, "New must fail when HealthCheck fails")
			assert.Contains(t, err.Error(), "failed to connect to Docker daemon", "Error must indicate connection failure")
		}
	})
}

func TestHealthCheck(t *testing.T) {
	t.Run("NoClient", func(t *testing.T) {
		backend := &DockerBackend{
			client: nil,
		}

		err := backend.HealthCheck(context.Background())
		assert.Error(t, err, "HealthCheck should fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized")
	})

	t.Run("Integration", func(t *testing.T) {
		backend, err := New(core.DockerConfig{})
		require.NoError(t, err, "Backend creation must succeed - Docker is required")
		require.NotNil(t, backend, "Backend must not be nil")

		defer func() {
			require.NotNil(t, backend.client, "Client must exist for cleanup")
			require.NoError(t, backend.client.Close(), "Client close must succeed")
		}()

		err = backend.HealthCheck(context.Background())
		require.NoError(t, err, "HealthCheck must succeed - Docker daemon must be running")
	})
}

func TestBackendImage(t *testing.T) {
	backend := &DockerBackend{}

	image := backend.Image("alpine:latest")
	assert.NotNil(t, image, "Image should return a non-nil image")

	dockerImage, ok := image.(*dockerImage)
	require.True(t, ok, "Image should return *dockerImage")
	assert.Equal(t, "alpine:latest", dockerImage.name)
	assert.Equal(t, backend, dockerImage.backend)
}
