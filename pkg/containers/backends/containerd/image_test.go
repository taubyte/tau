//go:build linux

package containerd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

func TestContainerdImage_Name(t *testing.T) {
	image := &containerdImage{
		name: "alpine:latest",
	}

	assert.Equal(t, "alpine:latest", image.Name())
}

func TestContainerdBackend_Image(t *testing.T) {
	backend := &ContainerdBackend{}

	image := backend.Image("alpine:latest")
	require.NotNil(t, image, "Image must not be nil")
	assert.Equal(t, "alpine:latest", image.Name(), "Image name must match")
}

func TestContainerdImage_Exists(t *testing.T) {
	t.Run("NoClient", func(t *testing.T) {
		image := &containerdImage{
			backend: &ContainerdBackend{
				client: nil,
			},
			name: "alpine:latest",
		}

		exists := image.Exists(context.Background())
		assert.False(t, exists, "Exists should return false when client is nil")
	})

	t.Run("Integration", func(t *testing.T) {
		if testing.Short() {
			t.Skip("Skipping integration test in short mode")
		}

		backend, err := New(core.ContainerdConfig{})
		if err != nil {
			t.Skipf("Skipping test: containerd not available: %v", err)
		}
		require.NotNil(t, backend, "Backend must not be nil")

		image := backend.Image("alpine:latest")
		require.NotNil(t, image, "Image must not be nil")

		exists := image.Exists(context.Background())
		assert.IsType(t, false, exists, "Exists must return a boolean")
	})
}

func TestContainerdImage_Pull(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend, err := New(core.ContainerdConfig{})
	if err != nil {
		t.Skipf("Skipping test: containerd not available: %v", err)
	}
	require.NotNil(t, backend, "Backend must not be nil")

	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		image := backend.Image("alpine:latest")
		require.NotNil(t, image, "Image must not be nil")

		if image.Exists(ctx) {
			image.Remove(ctx)
		}

		err = image.Pull(ctx)
		require.NoError(t, err, "Image pull must succeed")
		require.True(t, image.Exists(ctx), "Image must exist after pull")

		defer func() {
			if image.Exists(ctx) {
				image.Remove(ctx)
			}
		}()
	})

	t.Run("NoClient", func(t *testing.T) {
		image := &containerdImage{
			backend: &ContainerdBackend{
				client: nil,
			},
			name: "alpine:latest",
		}

		err := image.Pull(context.Background())
		assert.Error(t, err, "Pull must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized", "Error must mention client not initialized")
	})
}

func TestContainerdImage_Build(t *testing.T) {
	image := &containerdImage{
		name: "test:latest",
	}

	ctx := context.Background()
	err := image.Build(ctx, nil)
	assert.Error(t, err, "Build must fail")
	assert.Equal(t, core.ErrBuildNotSupported, err, "Build must return ErrBuildNotSupported")
}

func TestContainerdImage_Remove(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend, err := New(core.ContainerdConfig{})
	if err != nil {
		t.Skipf("Skipping test: containerd not available: %v", err)
	}
	require.NotNil(t, backend, "Backend must not be nil")

	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		image := backend.Image("alpine:latest")
		require.NotNil(t, image, "Image must not be nil")

		if !image.Exists(ctx) {
			err = image.Pull(ctx)
			require.NoError(t, err, "Image pull must succeed for removal test")
		}

		err = image.Remove(ctx)
		require.NoError(t, err, "Image removal must succeed")
		assert.False(t, image.Exists(ctx), "Image must not exist after removal")
	})

	t.Run("NoClient", func(t *testing.T) {
		image := &containerdImage{
			backend: &ContainerdBackend{
				client: nil,
			},
			name: "alpine:latest",
		}

		err := image.Remove(context.Background())
		assert.Error(t, err, "Remove must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized", "Error must mention client not initialized")
	})
}

func TestContainerdImage_Digest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend, err := New(core.ContainerdConfig{})
	if err != nil {
		t.Skipf("Skipping test: containerd not available: %v", err)
	}
	require.NotNil(t, backend, "Backend must not be nil")

	ctx := context.Background()

	image := backend.Image("alpine:latest")
	require.NotNil(t, image, "Image must not be nil")

	if !image.Exists(ctx) {
		err = image.Pull(ctx)
		require.NoError(t, err, "Image pull must succeed")
	}

	defer func() {
		if image.Exists(ctx) {
			image.Remove(ctx)
		}
	}()

	digest, err := image.Digest(ctx)
	require.NoError(t, err, "Digest must succeed")
	assert.NotEmpty(t, digest, "Digest must not be empty")
	assert.NotContains(t, digest, "sha256:", "Digest must not contain sha256: prefix")
}

func TestContainerdImage_Tags(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend, err := New(core.ContainerdConfig{})
	if err != nil {
		t.Skipf("Skipping test: containerd not available: %v", err)
	}
	require.NotNil(t, backend, "Backend must not be nil")

	ctx := context.Background()

	image := backend.Image("alpine:latest")
	require.NotNil(t, image, "Image must not be nil")

	if !image.Exists(ctx) {
		err = image.Pull(ctx)
		require.NoError(t, err, "Image pull must succeed")
	}

	defer func() {
		if image.Exists(ctx) {
			image.Remove(ctx)
		}
	}()

	tags, err := image.Tags(ctx)
	require.NoError(t, err, "Tags must succeed")
	assert.NotEmpty(t, tags, "Tags must not be empty")
	assert.Contains(t, tags, "alpine:latest", "Tags must contain the image name")
}

func TestContainerdBackend_HealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Run("Success", func(t *testing.T) {
		backend, err := New(core.ContainerdConfig{})
		if err != nil {
			t.Skipf("Skipping test: containerd not available: %v", err)
		}
		require.NotNil(t, backend, "Backend must not be nil")

		ctx := context.Background()
		err = backend.HealthCheck(ctx)
		assert.NoError(t, err, "HealthCheck must succeed when containerd is available")
	})

	t.Run("NoClient", func(t *testing.T) {
		backend := &ContainerdBackend{
			client: nil,
		}

		ctx := context.Background()
		err := backend.HealthCheck(ctx)
		assert.Error(t, err, "HealthCheck must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized", "Error must mention client not initialized")
	})
}

func TestContainerdBackend_Stop(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	backend, err := New(core.ContainerdConfig{})
	if err != nil {
		t.Skipf("Skipping test: containerd not available: %v", err)
	}
	require.NotNil(t, backend, "Backend must not be nil")

	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		containerConfig := &core.ContainerConfig{
			Image:   "quay.io/libpod/alpine:latest",
			Command: []string{"sh", "-c", "sleep 10"},
		}

		containerID, err := backend.Create(ctx, containerConfig)
		require.NoError(t, err, "Container creation should succeed")
		require.NotEmpty(t, containerID, "Container ID should not be empty")

		defer func() {
			backend.Remove(ctx, containerID)
		}()

		err = backend.Start(ctx, containerID)
		require.NoError(t, err, "Container start should succeed")

		err = backend.Stop(ctx, containerID)
		assert.NoError(t, err, "Container stop should succeed")

		info, err := backend.Inspect(ctx, containerID)
		require.NoError(t, err, "Container inspection should succeed")
		assert.NotEqual(t, "running", info.Status, "Container should not be running after stop")
	})

	t.Run("NoClient", func(t *testing.T) {
		backend := &ContainerdBackend{
			client: nil,
		}

		ctx := context.Background()
		err := backend.Stop(ctx, "test-container")
		assert.Error(t, err, "Stop must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized", "Error must mention client not initialized")
	})

	t.Run("ContainerNotStarted", func(t *testing.T) {
		containerConfig := &core.ContainerConfig{
			Image:   "quay.io/libpod/alpine:latest",
			Command: []string{"echo", "test"},
		}

		containerID, err := backend.Create(ctx, containerConfig)
		require.NoError(t, err, "Container creation should succeed")

		defer func() {
			backend.Remove(ctx, containerID)
		}()

		err = backend.Stop(ctx, containerID)
		assert.NoError(t, err, "Stop should succeed even if container is not started")
	})
}

func TestContainerdBackend_createOCISpec(t *testing.T) {
	backend := &ContainerdBackend{}

	t.Run("Basic", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image:   "alpine:latest",
			Command: []string{"echo", "test"},
		}

		spec, err := backend.createOCISpec(config)
		require.NoError(t, err, "createOCISpec should succeed")
		require.NotNil(t, spec, "Spec should not be nil")
		assert.Equal(t, "1.0.2", spec.Version, "Version should match")
		assert.Equal(t, "tau-container", spec.Hostname, "Hostname should match")
		assert.Equal(t, []string{"echo", "test"}, spec.Process.Args, "Command should match")
		assert.Equal(t, "/", spec.Process.Cwd, "Default workdir should be /")
	})

	t.Run("WithEnv", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image:   "alpine:latest",
			Command: []string{"sh"},
			Env:     []string{"TEST=value", "FOO=bar"},
		}

		spec, err := backend.createOCISpec(config)
		require.NoError(t, err, "createOCISpec should succeed")
		assert.Contains(t, spec.Process.Env, "TEST=value", "Env should contain TEST")
		assert.Contains(t, spec.Process.Env, "FOO=bar", "Env should contain FOO")
	})

	t.Run("WithWorkDir", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image:   "alpine:latest",
			Command: []string{"sh"},
			WorkDir: "/app",
		}

		spec, err := backend.createOCISpec(config)
		require.NoError(t, err, "createOCISpec should succeed")
		assert.Equal(t, "/app", spec.Process.Cwd, "WorkDir should match")
	})

	t.Run("WithResources", func(t *testing.T) {
		config := &core.ContainerConfig{
			Image:   "alpine:latest",
			Command: []string{"sh"},
			Resources: &core.ResourceLimits{
				Memory:    1024 * 1024 * 1024, // 1GB
				CPUQuota:  50000,
				CPUPeriod: 100000,
				PIDs:      100,
			},
		}

		spec, err := backend.createOCISpec(config)
		require.NoError(t, err, "createOCISpec should succeed")
		require.NotNil(t, spec.Linux.Resources, "Resources should not be nil")
		assert.NotNil(t, spec.Linux.Resources.Memory, "Memory should be set")
		assert.Equal(t, int64(1024*1024*1024), *spec.Linux.Resources.Memory.Limit, "Memory limit should match")
		assert.NotNil(t, spec.Linux.Resources.CPU, "CPU should be set")
		assert.Equal(t, int64(50000), *spec.Linux.Resources.CPU.Quota, "CPU quota should match")
		assert.Equal(t, uint64(100000), *spec.Linux.Resources.CPU.Period, "CPU period should match")
		assert.NotNil(t, spec.Linux.Resources.Pids, "PIDs should be set")
		assert.Equal(t, int64(100), spec.Linux.Resources.Pids.Limit, "PIDs limit should match")
	})
}

func TestContainerdBackend_hasSubIDMapping(t *testing.T) {
	backend := &ContainerdBackend{}

	t.Run("FileNotFound", func(t *testing.T) {
		err := backend.hasSubIDMapping("/nonexistent/file", "testuser")
		assert.Error(t, err, "hasSubIDMapping should fail when file doesn't exist")
		assert.Contains(t, err.Error(), "cannot read", "Error should mention file read failure")
	})

	t.Run("InvalidFormat", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test-subuid-*")
		require.NoError(t, err, "Should create temp file")
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString("invalid:format\n")
		require.NoError(t, err, "Should write to temp file")
		tmpFile.Close()

		err = backend.hasSubIDMapping(tmpFile.Name(), "testuser")
		assert.Error(t, err, "hasSubIDMapping should fail when no mapping found")
	})

	t.Run("ValidMapping", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test-subuid-*")
		require.NoError(t, err, "Should create temp file")
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString("testuser:100000:65536\n")
		require.NoError(t, err, "Should write to temp file")
		tmpFile.Close()

		err = backend.hasSubIDMapping(tmpFile.Name(), "testuser")
		assert.NoError(t, err, "hasSubIDMapping should succeed when mapping exists")
	})

	t.Run("CommentLine", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test-subuid-*")
		require.NoError(t, err, "Should create temp file")
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString("# This is a comment\ntestuser:100000:65536\n")
		require.NoError(t, err, "Should write to temp file")
		tmpFile.Close()

		err = backend.hasSubIDMapping(tmpFile.Name(), "testuser")
		assert.NoError(t, err, "hasSubIDMapping should ignore comment lines")
	})
}

func TestContainerdBackend_ensureContainerdRunning(t *testing.T) {
	t.Run("RootlessModeDisabled_NoSocket", func(t *testing.T) {
		backend := &ContainerdBackend{
			config: core.ContainerdConfig{
				RootlessMode: core.RootlessModeDisabled,
				SocketPath:   filepath.Join(t.TempDir(), "containerd.sock"),
			},
		}
		ctx := context.Background()
		err := backend.ensureContainerdRunning(ctx)
		require.Error(t, err, "ensureContainerdRunning should fail when socket doesn't exist")
		assert.Contains(t, err.Error(), "not running", "Error should mention containerd not running")
	})

	t.Run("RootlessModeDisabled_AutoStartDisabled", func(t *testing.T) {
		backend := &ContainerdBackend{
			config: core.ContainerdConfig{
				RootlessMode: core.RootlessModeDisabled,
				AutoStart:    false,
				SocketPath:   filepath.Join(t.TempDir(), "containerd.sock"),
			},
		}
		ctx := context.Background()
		err := backend.ensureContainerdRunning(ctx)
		require.Error(t, err, "ensureContainerdRunning should fail when socket doesn't exist and AutoStart is disabled")
	})
}

func TestContainerdBackend_Logs(t *testing.T) {
	t.Run("NoClient", func(t *testing.T) {
		backend := &ContainerdBackend{
			client: nil,
		}

		ctx := context.Background()
		logs, err := backend.Logs(ctx, "test-container")
		assert.Error(t, err, "Logs must fail when client is nil")
		assert.Nil(t, logs, "Logs should return nil when error occurs")
		assert.Contains(t, err.Error(), "not initialized", "Error must mention client not initialized")
	})

	t.Run("ContainerNotFound", func(t *testing.T) {
		backend := &ContainerdBackend{
			client: &Client{},
			tasks:  make(map[core.ContainerID]*taskIO),
		}

		ctx := context.Background()
		logs, err := backend.Logs(ctx, "nonexistent-container")
		assert.Error(t, err, "Logs must fail when container not found")
		assert.Nil(t, logs, "Logs should return nil when error occurs")
		assert.Contains(t, err.Error(), "not found", "Error must mention container not found")
	})
}

func TestContainerdBackend_Wait(t *testing.T) {
	t.Run("NoClient", func(t *testing.T) {
		backend := &ContainerdBackend{
			client: nil,
		}

		ctx := context.Background()
		err := backend.Wait(ctx, "test-container")
		assert.Error(t, err, "Wait must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized", "Error must mention client not initialized")
	})
}

func TestContainerdBackend_Remove(t *testing.T) {
	t.Run("NoClient", func(t *testing.T) {
		backend := &ContainerdBackend{
			client: nil,
		}

		ctx := context.Background()
		err := backend.Remove(ctx, "test-container")
		assert.Error(t, err, "Remove must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized", "Error must mention client not initialized")
	})
}

func TestContainerdBackend_Start(t *testing.T) {
	t.Run("NoClient", func(t *testing.T) {
		backend := &ContainerdBackend{
			client: nil,
		}

		ctx := context.Background()
		err := backend.Start(ctx, "test-container")
		assert.Error(t, err, "Start must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized", "Error must mention client not initialized")
	})
}

// Integration tests using containerd in Docker
func TestContainerdImage_NestedDocker_Pull(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc, cleanup := setupContainerdInDocker(t)
	defer cleanup()

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		SocketPath:   tc.socketPath,
		Namespace:    "tau-test-image-pull",
	})
	require.NoError(t, err)
	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

	ctx := context.Background()
	image := backend.Image("quay.io/libpod/alpine:latest")
	require.NotNil(t, image)

	// Remove if exists
	if image.Exists(ctx) {
		image.Remove(ctx)
	}

	err = image.Pull(ctx)
	require.NoError(t, err, "Image pull must succeed")
	require.True(t, image.Exists(ctx), "Image must exist after pull")

	defer func() {
		if image.Exists(ctx) {
			image.Remove(ctx)
		}
	}()
}

func TestContainerdImage_NestedDocker_Exists(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc, cleanup := setupContainerdInDocker(t)
	defer cleanup()

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		SocketPath:   tc.socketPath,
		Namespace:    "tau-test-image-exists",
	})
	require.NoError(t, err)
	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

	ctx := context.Background()
	image := backend.Image("quay.io/libpod/alpine:latest")
	require.NotNil(t, image)

	// Initially should not exist
	exists := image.Exists(ctx)
	assert.False(t, exists, "Image should not exist initially")

	// Pull the image
	err = image.Pull(ctx)
	require.NoError(t, err)

	// Now should exist
	exists = image.Exists(ctx)
	assert.True(t, exists, "Image should exist after pull")

	// Cleanup
	if image.Exists(ctx) {
		image.Remove(ctx)
	}
}

func TestContainerdImage_NestedDocker_Remove(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc, cleanup := setupContainerdInDocker(t)
	defer cleanup()

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		SocketPath:   tc.socketPath,
		Namespace:    "tau-test-image-remove",
	})
	require.NoError(t, err)
	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

	ctx := context.Background()
	image := backend.Image("quay.io/libpod/alpine:latest")
	require.NotNil(t, image)

	// Pull if not exists
	if !image.Exists(ctx) {
		err = image.Pull(ctx)
		require.NoError(t, err, "Image pull must succeed")
	}

	// Remove
	err = image.Remove(ctx)
	require.NoError(t, err, "Image removal must succeed")

	// Verify it's gone
	exists := image.Exists(ctx)
	assert.False(t, exists, "Image should not exist after removal")
}

func TestContainerdImage_NestedDocker_Digest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc, cleanup := setupContainerdInDocker(t)
	defer cleanup()

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		SocketPath:   tc.socketPath,
		Namespace:    "tau-test-image-digest",
	})
	require.NoError(t, err)
	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

	ctx := context.Background()
	image := backend.Image("quay.io/libpod/alpine:latest")
	require.NotNil(t, image)

	// Pull if not exists
	if !image.Exists(ctx) {
		err = image.Pull(ctx)
		require.NoError(t, err, "Image pull must succeed")
	}

	defer func() {
		if image.Exists(ctx) {
			image.Remove(ctx)
		}
	}()

	digest, err := image.Digest(ctx)
	require.NoError(t, err, "Digest must succeed")
	assert.NotEmpty(t, digest, "Digest must not be empty")
	assert.NotContains(t, digest, "sha256:", "Digest must not contain sha256: prefix")
	assert.Len(t, digest, 64, "Digest should be 64 characters (sha256 without prefix)")
}

func TestContainerdImage_NestedDocker_Tags(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc, cleanup := setupContainerdInDocker(t)
	defer cleanup()

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		SocketPath:   tc.socketPath,
		Namespace:    "tau-test-image-tags",
	})
	require.NoError(t, err)
	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

	ctx := context.Background()
	image := backend.Image("quay.io/libpod/alpine:latest")
	require.NotNil(t, image)

	// Pull if not exists
	if !image.Exists(ctx) {
		err = image.Pull(ctx)
		require.NoError(t, err, "Image pull must succeed")
	}

	defer func() {
		if image.Exists(ctx) {
			image.Remove(ctx)
		}
	}()

	tags, err := image.Tags(ctx)
	require.NoError(t, err, "Tags must succeed")
	assert.NotEmpty(t, tags, "Tags must not be empty")
	assert.Contains(t, tags, "quay.io/libpod/alpine:latest", "Tags must contain the image name")
}

func TestContainerdBackend_NestedDocker_HealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc, cleanup := setupContainerdInDocker(t)
	defer cleanup()

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		SocketPath:   tc.socketPath,
		Namespace:    "tau-test-healthcheck",
	})
	require.NoError(t, err)
	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

	ctx := context.Background()
	err = backend.HealthCheck(ctx)
	assert.NoError(t, err, "HealthCheck must succeed when containerd is available")
}

func TestContainerdBackend_NestedDocker_Stop(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc, cleanup := setupContainerdInDocker(t)
	defer cleanup()

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		SocketPath:   tc.socketPath,
		Namespace:    "tau-test-stop",
	})
	require.NoError(t, err)
	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

	ctx := context.Background()

	containerConfig := &core.ContainerConfig{
		Image:   "quay.io/libpod/alpine:latest",
		Command: []string{"sh", "-c", "sleep 10"},
	}

	containerID, err := backend.Create(ctx, containerConfig)
	require.NoError(t, err, "Container creation should succeed")
	require.NotEmpty(t, containerID, "Container ID should not be empty")

	defer func() {
		backend.Remove(ctx, containerID)
	}()

	err = backend.Start(ctx, containerID)
	require.NoError(t, err, "Container start should succeed")

	// Give it a moment to start
	time.Sleep(500 * time.Millisecond)

	// Verify it's running
	info, err := backend.Inspect(ctx, containerID)
	require.NoError(t, err, "Container inspection should succeed before stop")
	assert.Equal(t, "running", info.Status, "Container should be running before stop")

	// Stop the container - this should kill and delete the task
	err = backend.Stop(ctx, containerID)
	require.NoError(t, err, "Container stop should succeed")

	// Verify it's stopped
	info, err = backend.Inspect(ctx, containerID)
	require.NoError(t, err, "Container inspection should succeed after stop")
	assert.NotEqual(t, "running", info.Status, "Container should not be running after stop")
}

func TestContainerdBackend_NestedDocker_Start(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tc, cleanup := setupContainerdInDocker(t)
	defer cleanup()

	backend, err := New(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		SocketPath:   tc.socketPath,
		Namespace:    "tau-test-start",
	})
	require.NoError(t, err)
	defer func() {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
	}()

	ctx := context.Background()

	containerConfig := &core.ContainerConfig{
		Image:   "quay.io/libpod/alpine:latest",
		Command: []string{"echo", "test"},
	}

	containerID, err := backend.Create(ctx, containerConfig)
	require.NoError(t, err, "Container creation should succeed")

	defer func() {
		backend.Remove(ctx, containerID)
	}()

	err = backend.Start(ctx, containerID)
	require.NoError(t, err, "Container start should succeed")

	// Wait for it to finish
	err = backend.Wait(ctx, containerID)
	require.NoError(t, err, "Container should exit successfully")

	// Verify logs
	logs, err := backend.Logs(ctx, containerID)
	require.NoError(t, err, "Getting logs should succeed")
	defer logs.Close()

	logData, err := io.ReadAll(logs)
	require.NoError(t, err, "Reading logs should succeed")
	assert.Contains(t, string(logData), "test", "Logs should contain expected output")
}
