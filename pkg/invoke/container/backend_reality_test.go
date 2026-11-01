//go:build docker_integration

package container

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/backends/docker"
	"github.com/taubyte/tau/pkg/containers/core"
)

func TestTheConfigThisTierBuildsIsAcceptedByDocker(t *testing.T) {
	backend, err := docker.New(core.DockerConfig{})
	require.NoError(t, err, "no docker daemon to ask")

	// Exactly what the tier sends, built by the tier's own function.
	r := &Runtime{}
	cfg := r.config(Image{Ref: "docker.io/library/busybox:latest"}, nil, 18080, "", nil)

	require.NotNil(t, cfg.Network)
	t.Logf("tier asks for: mode=%q restrict-egress=%v port-mappings=%d",
		cfg.Network.Mode, cfg.Network.RestrictEgress, len(cfg.Network.PortMappings))

	// The image is not under test; a missing one would refuse the create for an
	// unrelated reason.
	image := backend.Image(cfg.Image)
	if !image.Exists(context.Background()) {
		require.NoError(t, image.Pull(context.Background()), "pulling the test image")
	}

	id, err := backend.Create(context.Background(), cfg)
	if err == nil {
		t.Cleanup(func() { backend.Remove(context.Background(), id) })
	}
	require.NoError(t, err, "the daemon refused the configuration this tier always sends")
}
