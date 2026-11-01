//go:build docker_integration

package live_test

import (
	"context"
	"fmt"
	"os/exec"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/backends/docker"
	"github.com/taubyte/tau/pkg/containers/core"
)

func TestARealForgeContainerTakesItsVolumesWithIt(t *testing.T) {
	cli, err := client.New(client.FromEnv)
	require.NoError(t, err)
	defer cli.Close()

	var volumes []string
	t.Run("started", func(t *testing.T) {
		c := start(t, &core.ContainerConfig{Image: giteaImage, Command: []string{"/bin/true"}})
		volumes = anonymousVolumes(t, cli, c.name)
	})
	requireGone(t, cli, volumes)
}

func TestARealSweptContainerTakesItsVolumesWithIt(t *testing.T) {
	cli, err := client.New(client.FromEnv)
	require.NoError(t, err)
	defer cli.Close()
	ctx := context.Background()

	dead := exec.Command("true")
	require.NoError(t, dead.Run())

	backend, err := docker.New(core.DockerConfig{})
	require.NoError(t, err)
	require.NoError(t, backend.Image(giteaImage).Pull(ctx))
	id, err := backend.Create(ctx, &core.ContainerConfig{
		Image:  giteaImage,
		Labels: map[string]string{ownerLabel: fmt.Sprint(dead.Process.Pid)},
	})
	require.NoError(t, err)
	volumes := anonymousVolumes(t, cli, string(id))

	sweep(t)
	_, err = cli.ContainerInspect(ctx, string(id), client.ContainerInspectOptions{})
	require.True(t, cerrdefs.IsNotFound(err), "the sweep left a dead run's container %s: %v", id, err)
	requireGone(t, cli, volumes)
}

// anonymousVolumes are the volumes docker made for a container from its
// image's VOLUME declarations.
func anonymousVolumes(t *testing.T, cli *client.Client, name string) []string {
	t.Helper()
	res, err := cli.ContainerInspect(context.Background(), name, client.ContainerInspectOptions{})
	require.NoError(t, err)
	var out []string
	for _, m := range res.Container.Mounts {
		if m.Type == mount.TypeVolume {
			out = append(out, m.Name)
		}
	}
	require.NotEmpty(t, out, "%s declares VOLUMEs, yet its container has no volume", giteaImage)
	t.Logf("%s has volumes %v", name, out)
	return out
}

func requireGone(t *testing.T, cli *client.Client, volumes []string) {
	t.Helper()
	require.NotEmpty(t, volumes)
	for _, v := range volumes {
		_, err := cli.VolumeInspect(context.Background(), v, client.VolumeInspectOptions{})
		require.True(t, cerrdefs.IsNotFound(err), "volume %s outlived its container: %v", v, err)
	}
}
