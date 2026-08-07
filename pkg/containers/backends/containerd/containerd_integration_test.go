//go:build linux && containerd_integration

// Integration tests for the containerd backend. These need a real containerd —
// either a system daemon at /run/containerd/containerd.sock, or containerd plus
// rootlesskit on PATH so a rootless one can be started. Run them with
// `make test-containerd`.

package containerd

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/backends/conformance"
	"github.com/taubyte/tau/pkg/containers/core"
)

// containerd resolves image names in full, unlike docker's shorthand.
const testImage = "docker.io/library/alpine:latest"

// newTestBackend connects to whatever containerd this machine has: the system
// daemon when it is reachable, otherwise a rootless one started for the test.
func newTestBackend(t *testing.T) *ContainerdBackend {
	t.Helper()

	config := core.ContainerdConfig{Namespace: "tau-test"}

	if systemContainerdReachable() {
		config.RootlessMode = core.RootlessModeDisabled
	} else {
		requireBinary(t, "containerd")
		requireBinary(t, "rootlesskit")
		config.RootlessMode = core.RootlessModeEnabled
		config.AutoStart = true
	}

	backend, err := New(config)
	require.NoError(t, err, "containerd must be available: start it system-wide, or install containerd+rootlesskit for the rootless path")

	t.Cleanup(func() {
		if backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
		if backend.daemon != nil {
			backend.daemon.Stop(context.Background())
		}
	})

	return backend
}

func systemContainerdReachable() bool {
	conn, err := net.Dial("unix", "/run/containerd/containerd.sock")
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func requireBinary(t *testing.T, name string) {
	t.Helper()
	daemon := &Daemon{}
	var err error
	switch name {
	case "containerd":
		_, err = daemon.findContainerdBinary()
	case "rootlesskit":
		_, err = daemon.findRootlesskitBinary()
	}
	require.NoError(t, err, "%s must be on PATH for the rootless path", name)
}

// TestConformance_Integration is the shared suite every backend must pass. It
// is what makes docker and containerd interchangeable rather than merely
// both present.
func TestConformance_Integration(t *testing.T) {
	conformance.Run(t, conformance.Backend{
		Backend: newTestBackend(t),
		Image:   testImage,
	})
}

func TestStopRunningContainer_Integration(t *testing.T) {
	conformance.RunStopsRunningContainer(t, conformance.Backend{
		Backend: newTestBackend(t),
		Image:   testImage,
	})
}

func TestImageLifecycle_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()
	image := backend.Image(testImage)

	require.NoError(t, image.Pull(ctx), "pull must succeed")
	assert.True(t, image.Exists(ctx), "a pulled image must exist locally")

	digest, err := image.Digest(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, digest)

	tags, err := image.Tags(ctx)
	require.NoError(t, err)
	assert.Contains(t, tags, testImage)

	require.NoError(t, image.Remove(ctx), "remove must succeed")
	assert.False(t, image.Exists(ctx), "a removed image must be gone")

	// Create pulls what is missing, so a removed image is not a broken run.
	id, err := backend.Create(ctx, &core.ContainerConfig{
		Image:   testImage,
		Command: []string{"/bin/true"},
	})
	require.NoError(t, err, "create must pull an image that is not present")
	require.NoError(t, backend.Remove(ctx, id))
}

func TestCreateDoesNotRepull_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()

	require.NoError(t, backend.Image(testImage).Pull(ctx))

	// With the image local, creating must not reach the registry — which also
	// means a build node keeps working when the registry does not.
	start := time.Now()
	id, err := backend.Create(ctx, &core.ContainerConfig{
		Image:   testImage,
		Command: []string{"/bin/true"},
	})
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 5*time.Second, "create must use the local image, not pull again")

	require.NoError(t, backend.Remove(ctx, id))
}

func TestRemoveReleasesResources_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()

	require.NoError(t, backend.Image(testImage).Pull(ctx))

	before := countTempFIFODirs(t)

	for i := 0; i < 5; i++ {
		id, err := backend.Create(ctx, &core.ContainerConfig{
			Image:   testImage,
			Command: []string{"/bin/sh", "-c", "echo run"},
		})
		require.NoError(t, err)
		require.NoError(t, backend.Start(ctx, id))
		require.NoError(t, backend.Wait(ctx, id))

		logs, err := backend.Logs(ctx, id)
		require.NoError(t, err)
		logs.Close()

		require.NoError(t, backend.Remove(ctx, id))
	}

	assert.Equal(t, before, countTempFIFODirs(t), "each removed container must take its FIFO directory with it")

	backend.mu.Lock()
	defer backend.mu.Unlock()
	assert.Empty(t, backend.tasks, "no task may outlive its container")
	assert.Empty(t, backend.containers, "no container handle may outlive its container")
}

func countTempFIFODirs(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir(os.TempDir())
	require.NoError(t, err)

	count := 0
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) > 20 && e.Name()[:20] == "tau-containerd-logs-" {
			count++
		}
	}
	return count
}

func TestHealthCheck_Integration(t *testing.T) {
	backend := newTestBackend(t)

	assert.NoError(t, backend.HealthCheck(context.Background()))
	assert.NoError(t, backend.testSocketConnection())
}
