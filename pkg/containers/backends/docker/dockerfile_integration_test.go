//go:build docker_integration

package docker

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

// TestReadDockerfileAgreesWithTheDaemon_Integration builds every context the
// unit test reads with a real daemon. Where readDockerfile returns a
// Dockerfile, the daemon built that one; where it returns none, the daemon
// built nothing. A context it refuses has nothing to agree on.
func TestReadDockerfileAgreesWithTheDaemon_Integration(t *testing.T) {
	backend, err := New(core.DockerConfig{})
	require.NoError(t, err)
	defer backend.client.Close()

	for i, c := range daemonContexts(t) {
		if c.refused != "" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			tag := fmt.Sprintf("tau-dockerfile-agrees:%d", i)
			err := backend.Image(tag).Build(t.Context(), &core.DockerfileBuild{
				Context:    bytes.NewReader(c.context),
				Dockerfile: c.dockerfile,
			})
			t.Cleanup(func() {
				backend.client.ImageRemove(context.Background(), tag, client.ImageRemoveOptions{Force: true, PruneChildren: true})
			})
			if c.pick == "" {
				require.Error(t, err, "the daemon built from a context with no Dockerfile read in it")
				return
			}
			require.NoError(t, err)

			built, err := backend.client.ImageInspect(t.Context(), tag)
			require.NoError(t, err)
			require.NotNil(t, built.Config)
			require.Equal(t, c.pick, built.Config.Labels["pick"], "the daemon built another Dockerfile than the one read")
		})
	}
}
