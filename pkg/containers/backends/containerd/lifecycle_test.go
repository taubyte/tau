//go:build linux

package containerd

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

// fakeTask scripts the three calls killAndWait makes. Anything else on the
// interface is unused and panics if reached.
type fakeTask struct {
	containerd.Task

	mu        sync.Mutex
	statuses  []containerd.ProcessStatus
	statusErr error
	waitErr   error
	exited    chan containerd.ExitStatus
	killErrs  map[syscall.Signal]error
	kills     []syscall.Signal
}

func (f *fakeTask) Status(context.Context) (containerd.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusErr != nil {
		return containerd.Status{}, f.statusErr
	}
	s := f.statuses[0]
	if len(f.statuses) > 1 {
		f.statuses = f.statuses[1:]
	}
	return containerd.Status{Status: s}, nil
}

func (f *fakeTask) Wait(context.Context) (<-chan containerd.ExitStatus, error) {
	return f.exited, f.waitErr
}

func (f *fakeTask) Kill(_ context.Context, sig syscall.Signal, _ ...containerd.KillOpts) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills = append(f.kills, sig)
	return f.killErrs[sig]
}

func running() []containerd.ProcessStatus { return []containerd.ProcessStatus{containerd.Running} }

func TestKillAndWait(t *testing.T) {
	ctx := context.Background()

	t.Run("a stopped task is left alone", func(t *testing.T) {
		task := &fakeTask{statuses: []containerd.ProcessStatus{containerd.Stopped}}
		require.NoError(t, killAndWait(ctx, task))
		assert.Empty(t, task.kills)
	})

	t.Run("SIGTERM and the exit arrives", func(t *testing.T) {
		exited := make(chan containerd.ExitStatus, 1)
		exited <- containerd.ExitStatus{}
		task := &fakeTask{statuses: running(), exited: exited}
		require.NoError(t, killAndWait(ctx, task))
		assert.Equal(t, []syscall.Signal{syscall.SIGTERM}, task.kills)
	})

	t.Run("a task reaped before the signal", func(t *testing.T) {
		task := &fakeTask{statuses: running(), killErrs: map[syscall.Signal]error{syscall.SIGTERM: errdefs.ErrNotFound}}
		require.NoError(t, killAndWait(ctx, task))
		assert.Equal(t, []syscall.Signal{syscall.SIGTERM}, task.kills)
	})

	t.Run("a refused SIGTERM escalates", func(t *testing.T) {
		exited := make(chan containerd.ExitStatus, 1)
		exited <- containerd.ExitStatus{}
		task := &fakeTask{statuses: running(), exited: exited, killErrs: map[syscall.Signal]error{syscall.SIGTERM: errors.New("refused")}}
		require.NoError(t, killAndWait(ctx, task))
		assert.Equal(t, []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}, task.kills)
	})

	t.Run("gone by the SIGKILL", func(t *testing.T) {
		task := &fakeTask{statuses: running(), killErrs: map[syscall.Signal]error{
			syscall.SIGTERM: errors.New("refused"), syscall.SIGKILL: errdefs.ErrNotFound,
		}}
		require.NoError(t, killAndWait(ctx, task))
	})

	t.Run("both signals refused", func(t *testing.T) {
		task := &fakeTask{statuses: running(), killErrs: map[syscall.Signal]error{
			syscall.SIGTERM: errors.New("refused"), syscall.SIGKILL: errors.New("still refused"),
		}}
		assert.ErrorContains(t, killAndWait(ctx, task), "still refused")
	})

	t.Run("no exit channel falls back to polling the status", func(t *testing.T) {
		task := &fakeTask{
			statuses: []containerd.ProcessStatus{containerd.Running, containerd.Running, containerd.Stopped},
			waitErr:  errors.New("no wait"),
		}
		require.NoError(t, killAndWait(ctx, task))
	})
}

func TestWaitUntilStopped(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, waitUntilStopped(ctx, &fakeTask{statusErr: errdefs.ErrNotFound}, time.Second),
		"a task that no longer exists has stopped")

	assert.ErrorContains(t, waitUntilStopped(ctx, &fakeTask{statusErr: errors.New("rpc down")}, time.Second), "rpc down")

	err := waitUntilStopped(ctx, &fakeTask{statuses: running()}, 120*time.Millisecond)
	assert.ErrorContains(t, err, "task did not stop within 120ms")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestResolveTaskPrefersItsOwn(t *testing.T) {
	task := &fakeTask{}
	backend := &ContainerdBackend{tasks: map[core.ContainerID]*taskIO{"c": {task: task}}}

	got, err := backend.resolveTask(context.Background(), "c")
	require.NoError(t, err)
	assert.Same(t, task, got)
}

func TestBuilderOutput(t *testing.T) {
	image := func(stdout, stderr string) *containerdImage {
		tio := &taskIO{stdout: io.NopCloser(strings.NewReader(stdout)), stderr: io.NopCloser(strings.NewReader(stderr))}
		tio.drain()
		backend := &ContainerdBackend{client: &Client{}, tasks: map[core.ContainerID]*taskIO{"b": tio}}
		return &containerdImage{backend: backend, name: "x"}
	}
	ctx := context.Background()

	assert.Equal(t, "error: step failed\nprogress", image("progress", "error: step failed").builderOutput(ctx, "b"))
	assert.Equal(t, "(builder produced no output)", image("", "").builderOutput(ctx, "b"))
	assert.Contains(t, image("", "").builderOutput(ctx, "missing"), "builder output unavailable")
}

func TestWithoutCgroups(t *testing.T) {
	spec := &specs.Spec{Linux: &specs.Linux{CgroupsPath: "/tau/x"}}
	require.NoError(t, withoutCgroups()(context.Background(), nil, nil, spec))
	assert.Empty(t, spec.Linux.CgroupsPath)

	require.NoError(t, withoutCgroups()(context.Background(), nil, nil, &specs.Spec{}), "a spec without a linux section is left as it is")
}

func TestBackendType(t *testing.T) {
	assert.Equal(t, core.BackendTypeContainerd, (&ContainerdBackend{}).BackendType())
}

func TestRegisteredFactoryRejectsAForeignConfig(t *testing.T) {
	factory, ok := core.GetBackendFactory(core.BackendTypeContainerd)
	require.True(t, ok)

	_, err := factory(core.DockerConfig{})
	assert.ErrorContains(t, err, "expected ContainerdConfig")
}

// fakeBin puts executable scripts on a PATH of their own, so what the daemon
// finds is exactly what the test provides.
func fakeBin(t *testing.T, scripts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	}
	t.Setenv("PATH", dir)
	return dir
}

func TestDetectRootlesskitNetwork(t *testing.T) {
	d := &Daemon{}

	fakeBin(t, nil)
	_, err := d.detectRootlesskitNetwork()
	assert.ErrorContains(t, err, "no suitable rootlesskit network driver")

	fakeBin(t, map[string]string{"vpnkit": ""})
	driver, err := d.detectRootlesskitNetwork()
	require.NoError(t, err)
	assert.Equal(t, "vpnkit", driver)

	fakeBin(t, map[string]string{"pasta": "", "vpnkit": ""})
	driver, err = d.detectRootlesskitNetwork()
	require.NoError(t, err)
	assert.Equal(t, "pasta", driver)

	// A slirp4netns too old to know --netns-type is passed over.
	fakeBin(t, map[string]string{"slirp4netns": "echo usage", "pasta": ""})
	driver, err = d.detectRootlesskitNetwork()
	require.NoError(t, err)
	assert.Equal(t, "pasta", driver)

	fakeBin(t, map[string]string{"slirp4netns": "echo --netns-type", "pasta": ""})
	driver, err = d.detectRootlesskitNetwork()
	require.NoError(t, err)
	assert.Equal(t, "slirp4netns", driver)
}

func TestDaemonStartNeedsItsBinaries(t *testing.T) {
	fakeBin(t, nil)

	err := newTestDaemon(t).Start(context.Background())
	assert.ErrorContains(t, err, "containerd binary not found")

	daemon := newTestDaemon(t)
	daemon.config.ContainerdPath = "/bin/true"
	assert.ErrorContains(t, daemon.Start(context.Background()), "rootlesskit is required")

	daemon.config.RootlesskitPath = "/bin/true"
	assert.ErrorContains(t, daemon.Start(context.Background()), "network driver")
}

// TestDaemonStartWithRootlesskit runs the whole rootless start with a stand-in
// rootlesskit that only sleeps. The test plays containerd by listening on the
// socket once the daemon has recorded the process, which is what Start waits for.
func TestDaemonStartWithRootlesskit(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("rootless start is refused to root")
	}

	t.Run("XDG directories given", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
		startWithStandIn(t)
	})

	t.Run("XDG directories defaulted under HOME", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		startWithStandIn(t)
	})
}

// startWithStandIn starts the daemon with the stand-in rootlesskit and checks
// what it left behind.
func startWithStandIn(t *testing.T) {
	bin := fakeBin(t, map[string]string{"pasta": "", "rootlesskit": `echo "$@" > "${0%/*}/args"; exec /bin/sleep 30`})
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(t.TempDir(), "run"))
	t.Setenv("TMPDIR", t.TempDir())

	daemon := newTestDaemon(t)
	daemon.config.ContainerdPath = "/bin/true"

	listening := make(chan net.Listener, 1)
	go func() {
		for {
			if _, err := os.Stat(daemon.stateFile); err == nil {
				ln, err := net.Listen("unix", daemon.socketPath)
				if err == nil {
					listening <- ln
				}
				close(listening)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	require.NoError(t, daemon.Start(context.Background()))
	ln, ok := <-listening
	require.True(t, ok)
	defer ln.Close()

	pid, err := os.ReadFile(daemon.stateFile)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(daemon.process.Pid), string(pid))
	assert.NoError(t, daemon.HealthCheck(context.Background()))

	args, err := os.ReadFile(filepath.Join(bin, "args"))
	require.NoError(t, err)
	assert.Contains(t, string(args), "--net pasta --mtu 65520")

	config, err := os.ReadFile(filepath.Join(filepath.Dir(daemon.socketPath), "containerd.toml"))
	require.NoError(t, err)
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(os.Getenv("HOME"), ".local", "share")
	}
	assert.Contains(t, string(config), filepath.Join(dataHome, "tau", "containerd", "daemon"))

	wrappers, err := filepath.Glob(filepath.Join(os.Getenv("TMPDIR"), "tau-containerd-rootless-*.sh"))
	require.NoError(t, err)
	assert.Empty(t, wrappers, "the wrapper script is removed once the daemon is up")

	process := daemon.process
	require.NoError(t, daemon.Stop(context.Background()))
	assert.Error(t, process.Signal(syscall.Signal(0)), "Stop must end the daemon process")
	assert.NoFileExists(t, daemon.stateFile)
	assert.NoFileExists(t, daemon.socketPath)
}

func TestDaemonStopFromPidFile(t *testing.T) {
	daemon := newTestDaemon(t)
	require.NoError(t, daemon.Stop(context.Background()), "stopping nothing is not an error")

	// A daemon this process did not start, known only by its pid file.
	sleeper, err := os.StartProcess("/bin/sleep", []string{"sleep", "30"}, &os.ProcAttr{})
	require.NoError(t, err)
	reaped := make(chan struct{})
	go func() {
		sleeper.Wait()
		close(reaped)
	}()
	require.NoError(t, os.WriteFile(daemon.stateFile, []byte(strconv.Itoa(sleeper.Pid)), 0o644))

	stranger := newTestDaemon(t)
	stranger.stateFile = daemon.stateFile
	stranger.socketPath = daemon.socketPath
	require.NoError(t, stranger.Stop(context.Background()))
	select {
	case <-reaped:
	case <-time.After(5 * time.Second):
		t.Fatal("the process named by the pid file is still running")
	}
	assert.NoFileExists(t, daemon.stateFile)
}

func TestDaemonStopAnsweringSocketWithStalePidFile(t *testing.T) {
	daemon := newTestDaemon(t)
	ln, err := net.Listen("unix", daemon.socketPath)
	require.NoError(t, err)
	defer ln.Close()
	require.NoError(t, os.WriteFile(daemon.stateFile, []byte("4194303"), 0o644))

	require.NoError(t, daemon.Stop(context.Background()))
	assert.NoFileExists(t, daemon.stateFile)
	assert.NoFileExists(t, daemon.socketPath)
}

func TestDaemonStartWhenAlreadyRunning(t *testing.T) {
	daemon := newTestDaemon(t)
	ln, err := net.Listen("unix", daemon.socketPath)
	require.NoError(t, err)
	defer ln.Close()

	assert.NoError(t, daemon.Start(context.Background()), "a daemon answering on the socket is not started twice")
	assert.Nil(t, daemon.process)
}

func TestNewDaemonRootfulUsesTheSystemPaths(t *testing.T) {
	if _, err := os.Stat("/run/containerd"); err != nil && os.Geteuid() != 0 {
		t.Skip("/run/containerd is missing and only root may create it")
	}

	daemon, err := NewDaemon(core.ContainerdConfig{RootlessMode: core.RootlessModeDisabled})
	require.NoError(t, err)
	assert.Equal(t, "/run/containerd/containerd.sock", daemon.socketPath)
	assert.Equal(t, "/run/containerd/containerd.pid", daemon.stateFile)
}

func TestNewDaemonUnwritableSocketDirectory(t *testing.T) {
	_, err := NewDaemon(core.ContainerdConfig{SocketPath: "/proc/self/nope/containerd.sock"})
	assert.ErrorContains(t, err, "failed to create directory")
}

func TestNewFailures(t *testing.T) {
	t.Run("no daemon at the system socket", func(t *testing.T) {
		_, err := New(core.ContainerdConfig{
			RootlessMode: core.RootlessModeDisabled,
			SocketPath:   filepath.Join(t.TempDir(), "containerd.sock"),
		})
		assert.ErrorContains(t, err, "failed to ensure containerd is running")
	})

	t.Run("through the registered factory", func(t *testing.T) {
		factory, ok := core.GetBackendFactory(core.BackendTypeContainerd)
		require.True(t, ok)
		_, err := factory(core.ContainerdConfig{
			RootlessMode: core.RootlessModeDisabled,
			SocketPath:   filepath.Join(t.TempDir(), "containerd.sock"),
		})
		assert.ErrorContains(t, err, "failed to ensure containerd is running")
	})

	t.Run("a socket file nothing answers on", func(t *testing.T) {
		socket := filepath.Join(t.TempDir(), "containerd.sock")
		ln, err := net.Listen("unix", socket)
		require.NoError(t, err)
		ln.(*net.UnixListener).SetUnlinkOnClose(false)
		ln.Close()

		_, err = New(core.ContainerdConfig{RootlessMode: core.RootlessModeDisabled, SocketPath: socket})
		assert.ErrorContains(t, err, "containerd not running at system socket")
	})

	t.Run("rootless as root", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("only root is refused rootless mode")
		}
		_, err := New(core.ContainerdConfig{RootlessMode: core.RootlessModeEnabled})
		assert.ErrorContains(t, err, "cannot enable rootless mode when running as root")
	})

	t.Run("a daemon manager that cannot be made", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root is never rootless")
		}
		_, err := New(core.ContainerdConfig{
			RootlessMode: core.RootlessModeEnabled,
			AutoStart:    true,
			SocketPath:   "/proc/self/nope/containerd.sock",
		})
		assert.ErrorContains(t, err, "failed to create daemon manager")
	})
}

func TestBuildRejectsABadContext(t *testing.T) {
	image := &containerdImage{backend: &ContainerdBackend{client: &Client{}}, name: "x"}
	err := image.Build(context.Background(), &core.DockerfileBuild{Context: strings.NewReader("not a tarball")})
	assert.ErrorContains(t, err, "failed to unpack build context")
}

func TestNetworkSpecOptsRefusals(t *testing.T) {
	_, err := networkSpecOpts(&core.NetworkConfig{DNS: []string{"1.1.1.1"}})
	assert.ErrorContains(t, err, "custom DNS servers not supported")

	_, err = networkSpecOpts(&core.NetworkConfig{PortMappings: []core.PortMapping{{HostPort: 8080, ContainerPort: 80}}})
	assert.ErrorContains(t, err, "cannot map port 80 to 8080")

	opts, err := networkSpecOpts(&core.NetworkConfig{PortMappings: []core.PortMapping{{HostPort: 80, ContainerPort: 80}}})
	require.NoError(t, err, "host networking already gives a container its own port")
	assert.NotEmpty(t, opts)
}

func TestDaemonStartFailures(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("rootless start is refused to root")
	}
	bin := fakeBin(t, map[string]string{"pasta": "", "rootlesskit": "exit 0"})
	home := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "run"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))

	start := func(t *testing.T, ctx context.Context, rootlesskit string) error {
		daemon := newTestDaemon(t)
		daemon.config.ContainerdPath = "/bin/true"
		daemon.config.RootlesskitPath = rootlesskit
		return daemon.Start(ctx)
	}

	t.Run("data directory cannot be made", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "/proc/self/nope")
		assert.ErrorContains(t, start(t, context.Background(), filepath.Join(bin, "rootlesskit")), "failed to create directory")
	})

	t.Run("wrapper cannot be written", func(t *testing.T) {
		daemon := newTestDaemon(t)
		daemon.config.ContainerdPath = "/bin/true"
		daemon.config.RootlesskitPath = filepath.Join(bin, "rootlesskit")
		t.Setenv("TMPDIR", "/proc/self/nope")
		assert.ErrorContains(t, daemon.Start(context.Background()), "failed to create rootlesskit wrapper")
	})

	t.Run("rootlesskit cannot be executed", func(t *testing.T) {
		notExecutable := filepath.Join(t.TempDir(), "rootlesskit")
		require.NoError(t, os.WriteFile(notExecutable, nil, 0o644))
		assert.ErrorContains(t, start(t, context.Background(), notExecutable), "failed to start containerd via rootlesskit")
	})

	t.Run("containerd never answers", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		daemon := newTestDaemon(t)
		daemon.config.ContainerdPath = "/bin/true"
		daemon.config.RootlesskitPath = filepath.Join(bin, "rootlesskit")

		assert.ErrorContains(t, daemon.Start(ctx), "containerd failed to start via rootlesskit")
		assert.NoFileExists(t, daemon.stateFile, "a daemon that never came up leaves no pid file")
	})
}

func TestNewDaemonRootlessRuntimeDirectory(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/proc/self/nope")
	_, err := NewDaemon(core.ContainerdConfig{RootlessMode: core.RootlessModeEnabled})
	assert.ErrorContains(t, err, "failed to create directory")

	runtime := filepath.Join("/run", "user", strconv.Itoa(os.Getuid()))
	if _, err := os.Stat(runtime); err != nil {
		t.Skip("no per-user runtime directory here")
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	daemon, err := NewDaemon(core.ContainerdConfig{RootlessMode: core.RootlessModeEnabled})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(runtime, "tau", "containerd", "containerd.sock"), daemon.socketPath)
}
