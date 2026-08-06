//go:build docker_integration

package docker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/backends/conformance"
	"github.com/taubyte/tau/pkg/containers/core"
)

const conformanceImage = "alpine:latest"

func newTestBackend(t *testing.T) *DockerBackend {
	t.Helper()

	backend, err := New(core.DockerConfig{})
	require.NoError(t, err, "the docker daemon must be running for these tests")

	t.Cleanup(func() { backend.client.Close() })

	return backend
}

// TestConformance_Integration is the shared suite every backend must pass. It
// is what makes docker and containerd interchangeable rather than merely both
// present.
func TestConformance_Integration(t *testing.T) {
	conformance.Run(t, conformance.Backend{
		Backend: newTestBackend(t),
		Image:   conformanceImage,
	})
}

func TestStopRunningContainer_Integration(t *testing.T) {
	conformance.RunStopsRunningContainer(t, conformance.Backend{
		Backend: newTestBackend(t),
		Image:   conformanceImage,
	})
}

func TestHealthCheckIntegration_Integration(t *testing.T) {
	require.NoError(t, newTestBackend(t).HealthCheck(context.Background()))
}
