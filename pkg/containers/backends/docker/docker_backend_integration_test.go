//go:build docker_integration

package docker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

func TestHealthCheckIntegration_Integration(t *testing.T) {
	backend, err := New(core.DockerConfig{})
	require.NoError(t, err, "Backend creation must succeed - Docker is required")
	require.NotNil(t, backend, "Backend must not be nil")

	defer func() {
		require.NotNil(t, backend.client, "Client must exist for cleanup")
		require.NoError(t, backend.client.Close(), "Client close must succeed")
	}()

	err = backend.HealthCheck(context.Background())
	require.NoError(t, err, "HealthCheck must succeed - Docker daemon must be running")
}
