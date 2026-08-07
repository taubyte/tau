//go:build linux

// Unit tests for daemon lifecycle bookkeeping. Every test points the daemon at
// a temp directory: writing to the real per-user socket path would fight with,
// and can kill, a containerd the developer is actually using.

package containerd

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

func newTestDaemon(t *testing.T) *Daemon {
	t.Helper()

	daemon, err := NewDaemon(core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
		SocketPath:   filepath.Join(t.TempDir(), "containerd.sock"),
	})
	require.NoError(t, err)

	return daemon
}

func TestNewDaemonPaths(t *testing.T) {
	t.Run("explicit socket path is used in either mode", func(t *testing.T) {
		for _, mode := range []core.RootlessMode{core.RootlessModeEnabled, core.RootlessModeDisabled} {
			socket := filepath.Join(t.TempDir(), "containerd.sock")

			daemon, err := NewDaemon(core.ContainerdConfig{RootlessMode: mode, SocketPath: socket})
			require.NoError(t, err)

			assert.Equal(t, socket, daemon.socketPath)
			assert.Equal(t, filepath.Join(filepath.Dir(socket), "containerd.pid"), daemon.stateFile,
				"the pid file must sit beside the socket it describes")
		}
	})

	t.Run("rootless defaults to a per-user path", func(t *testing.T) {
		daemon, err := NewDaemon(core.ContainerdConfig{RootlessMode: core.RootlessModeEnabled})
		require.NoError(t, err)

		assert.Contains(t, daemon.socketPath, "/tau/containerd/containerd.sock")
		assert.NotEqual(t, "/run/containerd/containerd.sock", daemon.socketPath,
			"a rootless daemon must not claim the system socket")
	})
}

func TestDaemonStartRefusesRootful(t *testing.T) {
	// Rootful containerd belongs to systemd; starting one here would fight it.
	daemon, err := NewDaemon(core.ContainerdConfig{
		RootlessMode: core.RootlessModeDisabled,
		AutoStart:    true,
		SocketPath:   filepath.Join(t.TempDir(), "containerd.sock"),
	})
	require.NoError(t, err)

	err = daemon.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "systemd")
}

func TestDaemonFindContainerdBinary(t *testing.T) {
	t.Run("configured path is returned as-is", func(t *testing.T) {
		daemon := &Daemon{config: core.ContainerdConfig{ContainerdPath: "/custom/containerd"}}

		path, err := daemon.findContainerdBinary()
		require.NoError(t, err)
		assert.Equal(t, "/custom/containerd", path)
	})

	t.Run("falls back to PATH", func(t *testing.T) {
		// Empty PATH means nothing is findable, whatever the machine has.
		t.Setenv("PATH", t.TempDir())

		_, err := (&Daemon{}).findContainerdBinary()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found in PATH")
	})
}

func TestDaemonFindRootlesskitBinary(t *testing.T) {
	t.Run("configured path is returned as-is", func(t *testing.T) {
		daemon := &Daemon{config: core.ContainerdConfig{RootlesskitPath: "/custom/rootlesskit"}}

		path, err := daemon.findRootlesskitBinary()
		require.NoError(t, err)
		assert.Equal(t, "/custom/rootlesskit", path)
	})

	t.Run("missing binary is reported", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())

		_, err := (&Daemon{}).findRootlesskitBinary()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found in PATH")
	})
}

func TestDaemonCreateConfigFile(t *testing.T) {
	dir := t.TempDir()
	daemon := newTestDaemon(t)

	path, err := daemon.createConfigFile(
		filepath.Join(dir, "root"),
		filepath.Join(dir, "state"),
		"/run/containerd/containerd.sock",
		"/run/containerd/debug.sock",
		filepath.Join(dir, "config"),
	)
	require.NoError(t, err)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	config := string(content)

	// The socket address is the one setting the client depends on: if it does
	// not land in the config, the daemon listens somewhere nobody looks.
	assert.Contains(t, config, `address = "/run/containerd/containerd.sock"`)
	assert.Contains(t, config, filepath.Join(dir, "root"))
	assert.Contains(t, config, filepath.Join(dir, "state"))
	assert.Contains(t, config, `disabled_plugins = ["io.containerd.grpc.v1.cri"]`,
		"CRI is dead weight here and slows startup")
}

func TestDaemonIsRunning(t *testing.T) {
	daemon := newTestDaemon(t)

	assert.False(t, daemon.isRunning(), "nothing has been started")

	// A stale pid file from a dead process must not read as running.
	require.NoError(t, os.WriteFile(daemon.stateFile, []byte("4194303"), 0644))
	assert.False(t, daemon.isRunning(), "a pid that no longer exists is not a running daemon")

	// Our own pid does exist, and is signalable.
	require.NoError(t, os.WriteFile(daemon.stateFile, []byte(strconv.Itoa(os.Getpid())), 0644))
	assert.True(t, daemon.isRunning())
}

func TestDaemonWaitForSocket(t *testing.T) {
	daemon := newTestDaemon(t)

	err := daemon.waitForSocket(context.Background(), 50*time.Millisecond)
	require.Error(t, err, "a socket that never appears must time out")
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

func TestDaemonHealthCheck(t *testing.T) {
	err := newTestDaemon(t).HealthCheck(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestDaemonConnectToSocket(t *testing.T) {
	daemon := newTestDaemon(t)

	_, err := daemon.connectToSocket()
	assert.Error(t, err, "there is no socket to connect to")

	// A plain file at the path is not a socket.
	require.NoError(t, os.WriteFile(daemon.socketPath, nil, 0644))
	_, err = daemon.connectToSocket()
	assert.Error(t, err)
	assert.False(t, daemon.isSocketReady())
}
