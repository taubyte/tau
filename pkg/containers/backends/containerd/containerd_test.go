//go:build linux

package containerd

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

func skipIfSystemContainerdUnavailable(t *testing.T, socketPath string, dialErr error) {
	if dialErr == nil {
		return
	}
	errStr := dialErr.Error()
	if strings.Contains(errStr, "permission denied") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no such file") {
		t.Skipf("System containerd at %s not accessible (run as root or in containerd group): %v", socketPath, dialErr)
	}
	require.NoError(t, dialErr, "System containerd must be running at %s for this test", socketPath)
}

func waitForExecCompletion(t *testing.T, ctx context.Context, dockerClient *client.Client, execID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		select {
		case <-ticker.C:
			insp, err := dockerClient.ContainerExecInspect(ctx, execID)
			if err != nil {
				continue
			}
			if !insp.Running {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func TestContainerdBackend_detectRootlessMode(t *testing.T) {
	config := core.ContainerdConfig{}

	backend := &ContainerdBackend{
		config: config,
	}

	err := backend.detectRootlessMode()
	if err != nil {
		t.Fatalf("detectRootlessMode failed: %v", err)
	}

	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() failed: %v", err)
	}

	isRoot := currentUser.Uid == "0"
	var expectedRootless core.RootlessMode
	if isRoot {
		expectedRootless = core.RootlessModeDisabled
	} else {
		expectedRootless = core.RootlessModeEnabled
	}

	if backend.config.RootlessMode != expectedRootless {
		t.Errorf("Expected rootless mode %v, got %v", expectedRootless, backend.config.RootlessMode)
	}

	t.Logf("Auto-detected rootless mode: %v (current user: %s, uid: %s)",
		backend.config.RootlessMode, currentUser.Username, currentUser.Uid)
}

func TestContainerdBackend_detectRootlessMode_Explicit(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	err := backend.detectRootlessMode()
	if err != nil {
		t.Fatalf("detectRootlessMode failed: %v", err)
	}

	if backend.config.RootlessMode != core.RootlessModeEnabled {
		t.Errorf("Expected explicit rootless mode to be preserved")
	}
}

func TestContainerdBackend_detectRootlessMode_ExplicitRoot(t *testing.T) {
	// Test explicit root mode setting
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	err := backend.detectRootlessMode()
	if err != nil {
		t.Fatalf("detectRootlessMode failed: %v", err)
	}

	if backend.config.RootlessMode != core.RootlessModeDisabled {
		t.Errorf("Expected explicit root mode to be preserved")
	}
}

func TestContainerdBackend_detectRootlessMode_Conflict(t *testing.T) {
	// Test conflict: rootless mode enabled but running as root
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() failed: %v", err)
	}

	if currentUser.Uid != "0" {
		t.Skip("This test requires root to verify rootless conflict (rootless mode enabled as root must error)")
	}
	err = backend.detectRootlessMode()
	require.Error(t, err, "Must error when enabling rootless mode as root user")
	t.Logf("Correctly detected conflict: %v", err)
}

func TestContainerdBackend_getSocketPath(t *testing.T) {
	// Test default socket path in rootless mode
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	socketPath, err := backend.getSocketPath()
	assert.NoError(t, err, "getSocketPath should succeed")
	// Should be /run/user/{uid}/tau/containerd/containerd.sock
	if !strings.HasPrefix(socketPath, "/run/user/") {
		t.Errorf("Expected socket path to start with /run/user/, got %s", socketPath)
	}
	if !strings.Contains(socketPath, "/tau/containerd/containerd.sock") {
		t.Errorf("Expected socket path to contain /tau/containerd/containerd.sock, got %s", socketPath)
	}

	t.Logf("Rootless socket path: %s", socketPath)
}

func TestContainerdBackend_getSocketPath_Explicit(t *testing.T) {
	// Test explicit socket path
	explicitPath := "/tmp/test.sock"
	config := core.ContainerdConfig{
		SocketPath: explicitPath,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	socketPath, err := backend.getSocketPath()
	assert.NoError(t, err, "getSocketPath should succeed")
	if socketPath != explicitPath {
		t.Errorf("Expected explicit socket path %s, got %s", explicitPath, socketPath)
	}
}

func TestContainerdBackend_getSocketPath_RootMode(t *testing.T) {
	// Test default socket path in root mode
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	socketPath, err := backend.getSocketPath()
	assert.NoError(t, err, "getSocketPath should succeed")
	expectedPath := "/run/containerd/containerd.sock"

	if socketPath != expectedPath {
		t.Errorf("Expected root socket path %s, got %s", expectedPath, socketPath)
	}
}

func TestContainerdBackend_isRootlessMode(t *testing.T) {
	tests := []struct {
		name         string
		rootlessMode core.RootlessMode
		expected     bool
	}{
		{"auto mode", core.RootlessModeAuto, false},
		{"disabled", core.RootlessModeDisabled, false},
		{"enabled", core.RootlessModeEnabled, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := core.ContainerdConfig{
				RootlessMode: tt.rootlessMode,
			}

			backend := &ContainerdBackend{
				config: config,
			}

			result := backend.isRootlessMode()
			if result != tt.expected {
				t.Errorf("isRootlessMode() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestContainerdBackend_validateUIDGIDMapping(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	// For rootless mode, UID/GID mapping validation should pass if subuid/subgid are configured
	// This is a critical requirement for rootless containers to work
	err := backend.validateUIDGIDMapping()
	assert.NoError(t, err, "UID/GID mapping validation should pass when subuid/subgid are configured for rootless mode")
}

func TestContainerdBackend_Capabilities(t *testing.T) {
	config := core.ContainerdConfig{}

	backend := &ContainerdBackend{
		config: config,
	}

	caps := backend.Capabilities()

	// Check that all capabilities are set correctly
	expectedCaps := core.BackendCapabilities{
		SupportsMemory:     true,
		SupportsCPU:        true,
		SupportsStorage:    true,
		SupportsPIDs:       true,
		SupportsMemorySwap: true,
		SupportsBuild:      true, // with BuildKit
		SupportsOCI:        true,
		SupportsNetworking: true,
		SupportsVolumes:    true,
	}

	if caps != expectedCaps {
		t.Errorf("Capabilities() = %+v, expected %+v", caps, expectedCaps)
	}
}

func TestContainerdBackend_testSocketConnection(t *testing.T) {
	backend := &ContainerdBackend{
		config: core.ContainerdConfig{
			RootlessMode: core.RootlessModeEnabled,
			Namespace:    "test",
		},
	}

	socketPath, err := backend.getSocketPath()
	assert.NoError(t, err, "getSocketPath should succeed")

	os.Remove(socketPath)
	err = backend.testSocketConnection()
	if err == nil {
		t.Fatalf("testSocketConnection should return an error for non-existent socket at %s", socketPath)
	}
	assert.Contains(t, err.Error(), "does not exist")
	socketDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		t.Fatalf("Failed to create socket directory: %v", err)
	}
	socketFile, err := os.Create(socketPath)
	assert.NoError(t, err)
	socketFile.Close()
	err = backend.testSocketConnection()
	assert.Error(t, err, "Socket connection should fail for fake file")
	assert.Contains(t, err.Error(), "failed to connect to socket")
	os.Remove(socketPath)
}

func TestContainerdBackend_RootfulMode_SocketPath(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
	}

	backend := &ContainerdBackend{
		config: config,
	}

	socketPath, err := backend.getSocketPath()
	assert.NoError(t, err, "getSocketPath should succeed in rootful mode")
	assert.Equal(t, "/run/containerd/containerd.sock", socketPath, "Rootful mode should use system socket path")
}

func TestContainerdBackend_RootfulMode_DoesNotStartDaemon(t *testing.T) {
	fakeSocket := filepath.Join(t.TempDir(), "containerd.sock")
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    true,
		SocketPath:   fakeSocket,
	}
	daemon, err := NewDaemon(config)
	assert.NoError(t, err, "NewDaemon should succeed")

	ctx := context.Background()
	err = daemon.Start(ctx)
	require.Error(t, err, "Start() must fail in rootful mode when socket is not ready")
	assert.Contains(t, err.Error(), "systemd", "Error should mention systemd")
	assert.Contains(t, err.Error(), "rootful", "Error should mention rootful mode")
}

func TestContainerdBackend_RootfulMode_BackendCreation_NoSystemContainerd(t *testing.T) {
	fakeSocket := filepath.Join(t.TempDir(), "containerd.sock")
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    false,
		Namespace:    "tau-test-rootful",
		SocketPath:   fakeSocket,
	}

	_, err := New(config)
	require.Error(t, err, "Backend creation must fail when containerd socket is not available")
	assert.Contains(t, err.Error(), "system-wide", "Error should mention system-wide containerd")
}

func TestContainerdBackend_RootfulMode_AutoStart_DoesNotStart(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    true,
		Namespace:    "tau-test-rootful",
	}
	backend, err := New(config)
	if err == nil {
		if backend != nil && backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
		assert.Nil(t, backend.daemon, "Daemon should not be initialized in rootful mode even with AutoStart")
		return
	}
	assert.Error(t, err, "Backend creation should fail when system containerd is not running")
	assert.Contains(t, err.Error(), "system-wide", "Error should mention system-wide containerd")
}

type containerdTestContainer struct {
	dockerClient *client.Client
	containerID  string
	socketPath   string
	tempDir      string
}

func setupContainerdInDocker(t *testing.T) (*containerdTestContainer, func()) {
	t.Helper()
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err, "Docker must be available for this test")
	ctx := context.Background()
	tempDir, err := os.MkdirTemp("", "tau-containerd-test-*")
	require.NoError(t, err, "Should create temp directory")
	socketPath := filepath.Join(tempDir, "containerd.sock")
	const linuxkitImage = "docker.io/linuxkit/containerd:latest"
	alpineImage := "docker.io/library/alpine:latest"

	useLinuxkit := false

	linuxkitList, err := dockerClient.ImageList(ctx, types.ImageListOptions{
		Filters: filters.NewArgs(filters.Arg("reference", linuxkitImage)),
	})
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to check for linuxkit/containerd image: %v", err)
	}
	if len(linuxkitList) > 0 {
		useLinuxkit = true
	} else {
		t.Logf("Pulling prebuilt containerd image: %s", linuxkitImage)
		reader, pullErr := dockerClient.ImagePull(ctx, linuxkitImage, types.ImagePullOptions{})
		if pullErr == nil {
			io.Copy(io.Discard, reader)
			reader.Close()
			t.Logf("Successfully pulled %s", linuxkitImage)
			useLinuxkit = true
			allList, _ := dockerClient.ImageList(ctx, types.ImageListOptions{})
			for i := range allList {
				for _, tag := range allList[i].RepoTags {
					if strings.Contains(tag, "linuxkit") && strings.Contains(tag, "containerd") {
						linuxkitList = allList[i : i+1]
						break
					}
				}
				if len(linuxkitList) > 0 {
					break
				}
			}
		} else {
			t.Logf("Prebuilt image not available (%v), falling back to Alpine", pullErr)
		}
	}
	var linuxkitImageID string
	if useLinuxkit && len(linuxkitList) > 0 {
		linuxkitImageID = linuxkitList[0].ID
	}
	if useLinuxkit && linuxkitImageID == "" {
		t.Logf("Could not resolve linuxkit/containerd image after pull, falling back to Alpine")
		useLinuxkit = false
	}

	var containerConfig *container.Config
	if useLinuxkit {
		containerConfig = &container.Config{
			Image: linuxkitImageID,
			Cmd: []string{
				"containerd",
				"--address", "/run/containerd/containerd.sock",
				"--config", "/etc/containerd/config.toml",
			},
			Tty: false,
		}
	} else {
		alpineList, listErr := dockerClient.ImageList(ctx, types.ImageListOptions{
			Filters: filters.NewArgs(filters.Arg("reference", alpineImage)),
		})
		if listErr != nil || len(alpineList) == 0 {
			t.Logf("Pulling Alpine image: %s", alpineImage)
			reader, pullErr := dockerClient.ImagePull(ctx, alpineImage, types.ImagePullOptions{})
			if pullErr != nil {
				os.RemoveAll(tempDir)
				t.Fatalf("Failed to pull Alpine image: %v", pullErr)
			}
			io.Copy(io.Discard, reader)
			reader.Close()
		}
		containerConfig = &container.Config{
			Image: alpineImage,
			Cmd: []string{
				"/bin/sh", "-c",
				`set -e
			apk add --no-cache containerd
			mkdir -p /run/containerd
			chmod 777 /run/containerd
			if [ -f /proc/self/cgroup ]; then
				CGROUP_PATH=$(cat /proc/self/cgroup | head -1 | cut -d: -f3)
				if [ -n "$CGROUP_PATH" ] && [ "$CGROUP_PATH" != "/" ]; then
					if [ -f /sys/fs/cgroup$CGROUP_PATH/cgroup.controllers ]; then
						cat /sys/fs/cgroup$CGROUP_PATH/cgroup.controllers | tr ' ' '\n' | while read controller; do
							echo "+$controller" > /sys/fs/cgroup$CGROUP_PATH/cgroup.subtree_control 2>/dev/null || true
						done
					fi
				fi
			fi
			mkdir -p /etc/containerd
			cat > /etc/containerd/config.toml <<'EOF'
version = 2
root = "/var/lib/containerd"
state = "/run/containerd"
disabled_plugins = ["io.containerd.grpc.v1.cri"]

[plugins."io.containerd.runtime.v2.task"]
  platforms = ["linux/amd64"]
  sched_core = false

[plugins."io.containerd.runtime.v2.task.options"]
  SystemdCgroup = false
  NoPivotRoot = false
EOF
			containerd --address /run/containerd/containerd.sock --config /etc/containerd/config.toml &
			CONTAINERD_PID=$!
			for i in $(seq 1 30); do
				if [ -S /run/containerd/containerd.sock ]; then
					chmod 666 /run/containerd/containerd.sock
					break
				fi
				sleep 0.5
			done
			wait $CONTAINERD_PID`,
			},
			Tty: false,
		}
	}

	hostConfig := &container.HostConfig{
		Privileged:   true,
		SecurityOpt:  []string{"apparmor=unconfined", "seccomp=unconfined"},
		CgroupnsMode: "host",
		Mounts: []mount.Mount{{
			Type:   mount.TypeBind,
			Source: tempDir,
			Target: "/run/containerd",
		}},
		Binds: []string{
			"/sys:/sys:ro",
			"/sys/fs/cgroup:/sys/fs/cgroup:rw",
			"/tmp:/tmp:rw",
			"/dev:/dev",
		},
		Tmpfs: map[string]string{"/var/lib/containerd": ""},
	}
	containerName := fmt.Sprintf("tau-containerd-test-%d", time.Now().UnixNano())
	resp, err := dockerClient.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, containerName)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to create containerd container: %v", err)
	}
	containerID := resp.ID
	err = dockerClient.ContainerStart(ctx, containerID, container.StartOptions{})
	if err != nil {
		dockerClient.ContainerRemove(ctx, containerID, container.RemoveOptions{})
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to start containerd container: %v", err)
	}

	t.Logf("Started containerd container: %s", containerID[:12])
	socketReady := false
	maxWait := 60 * time.Second
	if !useLinuxkit {
		maxWait = 3 * 60 * time.Second
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	waitCtx, cancel := context.WithTimeout(context.Background(), maxWait)
	defer cancel()

waitLoop:
	for !socketReady {
		select {
		case <-waitCtx.Done():
			break waitLoop
		case <-ticker.C:
			insp, err := dockerClient.ContainerInspect(ctx, containerID)
			if err == nil && insp.State.Status != "running" {
				break waitLoop
			}
			if stat, statErr := os.Stat(socketPath); statErr == nil {
				if stat.Mode().Perm()&0006 == 0 {
					os.Chmod(socketPath, 0666)
				}
				if conn, err := net.Dial("unix", socketPath); err == nil {
					conn.Close()
					socketReady = true
					t.Logf("Containerd socket is ready at %s", socketPath)
					break waitLoop
				}
			}
		}
	}

	if !socketReady {
		insp, _ := dockerClient.ContainerInspect(ctx, containerID)
		status := "unknown"
		exitCode := 0
		if insp.State != nil {
			status = insp.State.Status
			exitCode = insp.State.ExitCode
		}
		logsOpts := types.ContainerLogsOptions{ShowStdout: true, ShowStderr: true}
		if logStream, err := dockerClient.ContainerLogs(ctx, containerID, logsOpts); err == nil {
			logBuf := new(strings.Builder)
			io.Copy(logBuf, logStream)
			logStream.Close()
			t.Logf("Container logs:\n%s", logBuf.String())
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		dockerClient.ContainerStop(cleanupCtx, containerID, container.StopOptions{})
		dockerClient.ContainerRemove(cleanupCtx, containerID, container.RemoveOptions{})
		cleanupCancel()
		os.RemoveAll(tempDir)
		t.Fatalf("Containerd socket not ready after %v (container state: %s, exit code: %d); see container logs above", maxWait, status, exitCode)
	}

	tc := &containerdTestContainer{
		dockerClient: dockerClient,
		containerID:  containerID,
		socketPath:   socketPath,
		tempDir:      tempDir,
	}

	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		t.Logf("Cleaning up containerd test container: %s", tc.containerID[:12])
		cleanupCmd := []string{"sh", "-c", "rm -rf /run/containerd/*"}
		if execResp, err := dockerClient.ContainerExecCreate(ctx, tc.containerID, types.ExecConfig{
			Cmd: cleanupCmd, AttachStdout: false, AttachStderr: false,
		}); err == nil {
			dockerClient.ContainerExecStart(ctx, execResp.ID, types.ExecStartCheck{})
			waitForExecCompletion(t, ctx, dockerClient, execResp.ID, 5*time.Second)
		}
		dockerClient.ContainerStop(ctx, tc.containerID, container.StopOptions{})
		dockerClient.ContainerRemove(ctx, tc.containerID, container.RemoveOptions{RemoveVolumes: true, Force: true})
		os.RemoveAll(tc.tempDir)
	}

	return tc, cleanup
}
