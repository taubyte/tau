//go:build docker_integration

package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
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

// An image that declares a VOLUME gets a fresh anonymous volume in every
// container made from it, and nothing but that container's removal ever takes
// the volume away: left behind, one piles up per removal. A named volume the
// caller mounted is the caller's, and outlives the container.
func TestRemoveTakesTheAnonymousVolumesWithIt_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := t.Context()

	image := fmt.Sprintf("tau-anonymous-volume:%d", time.Now().UnixNano())
	dockerfile := "FROM " + conformanceImage + "\nVOLUME /data\n"
	require.NoError(t, buildClassic(t, backend, image, bytes.NewReader(contextOf(t, member{name: "Dockerfile", kind: tar.TypeReg, body: dockerfile})), "Dockerfile"))

	named := fmt.Sprintf("tau-named-volume-%d", time.Now().UnixNano())
	_, err := backend.client.VolumeCreate(ctx, client.VolumeCreateOptions{Name: named})
	require.NoError(t, err)
	t.Cleanup(func() {
		backend.client.VolumeRemove(context.Background(), named, client.VolumeRemoveOptions{Force: true})
	})

	id, err := backend.Create(ctx, &core.ContainerConfig{
		Image:   image,
		Command: []string{"/bin/sh", "-c", "echo kept > /data/f && echo kept > /named/f"},
		Volumes: []core.VolumeMount{{Source: named, Destination: "/named", IsVolumeName: true}},
	})
	require.NoError(t, err)
	require.NoError(t, backend.Start(ctx, id))
	require.NoError(t, backend.Wait(ctx, id))

	dockerID, err := backend.getDockerID(ctx, id)
	require.NoError(t, err)
	inspected, err := backend.client.ContainerInspect(ctx, dockerID, client.ContainerInspectOptions{})
	require.NoError(t, err)
	var anonymous string
	for _, m := range inspected.Container.Mounts {
		if m.Type == mount.TypeVolume && m.Destination == "/data" {
			anonymous = m.Name
		}
	}
	require.NotEmpty(t, anonymous, "the image's VOLUME is mounted from an anonymous volume")
	t.Cleanup(func() {
		backend.client.VolumeRemove(context.Background(), anonymous, client.VolumeRemoveOptions{Force: true})
	})

	require.NoError(t, backend.Remove(ctx, id))

	_, err = backend.client.VolumeInspect(ctx, anonymous, client.VolumeInspectOptions{})
	assert.True(t, errdefs.IsNotFound(err), "the anonymous volume %s outlived its container: %v", anonymous, err)
	_, err = backend.client.VolumeInspect(ctx, named, client.VolumeInspectOptions{})
	assert.NoError(t, err, "a named volume outlives the container it was mounted in")
}
