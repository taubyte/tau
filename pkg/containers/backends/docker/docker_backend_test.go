package docker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

func TestCapabilities(t *testing.T) {
	// Callers route Dockerfile builds by this flag, and the docker daemon is
	// the backend that can serve them.
	assert.True(t, (&DockerBackend{}).Capabilities().SupportsBuild)
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

func TestOperationsWithoutClient(t *testing.T) {
	// Every entry point must refuse rather than panic when the backend was
	// never connected. These need no daemon, so they run in the default suite.
	backend := &DockerBackend{}
	ctx := context.Background()

	_, err := backend.Create(ctx, &core.ContainerConfig{Image: "alpine"})
	assert.Error(t, err)
	assert.Error(t, backend.Start(ctx, "c"))
	assert.Error(t, backend.Stop(ctx, "c"))
	assert.Error(t, backend.Remove(ctx, "c"))
	assert.Error(t, backend.Wait(ctx, "c"))
	assert.Error(t, backend.Clean(ctx, time.Hour, nil))

	_, err = backend.Logs(ctx, "c")
	assert.Error(t, err)

	_, err = backend.Inspect(ctx, "c")
	assert.Error(t, err)
}

func TestImageWithoutClient(t *testing.T) {
	image := (&DockerBackend{}).Image("alpine:latest")

	assert.Equal(t, "alpine:latest", image.Name())
	assert.False(t, image.Exists(context.Background()))
	assert.Error(t, image.Pull(context.Background()))
	assert.Error(t, image.Remove(context.Background()))
	assert.Error(t, image.Build(context.Background(), &DockerBuildInput{}))

	_, err := image.Digest(context.Background())
	assert.Error(t, err)

	_, err = image.Tags(context.Background())
	assert.Error(t, err)
}

func TestGetDockerIDFromMap(t *testing.T) {
	// A known container resolves from the map without asking the daemon.
	backend := &DockerBackend{containers: map[core.ContainerID]string{"tau-1": "deadbeef"}}

	dockerID, err := backend.getDockerID(context.Background(), "tau-1")
	require.NoError(t, err)
	assert.Equal(t, "deadbeef", dockerID)

	backend.forgetContainer("tau-1")
	_, ok := backend.lookupContainer("tau-1")
	assert.False(t, ok, "a forgotten container must not resolve")
}
