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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd"
	namespacesapi "github.com/containerd/containerd/api/services/namespaces/v1"
	versionapi "github.com/containerd/containerd/api/services/version/v1"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
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

	// A builder still talking when the caller gives up must not hold up the
	// report of why the build failed.
	talking, open := io.Pipe()
	defer open.Close()
	tio := &taskIO{stdout: talking}
	tio.drain()
	still := &containerdImage{backend: &ContainerdBackend{client: &Client{}, tasks: map[core.ContainerID]*taskIO{"b": tio}}, name: "x"}

	gone, cancel := context.WithCancel(ctx)
	cancel()
	assert.Equal(t, "(builder output unreadable: context canceled)", still.builderOutput(gone, "b"))
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
		t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "run"))
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
		args := startWithStandIn(t, "pasta")
		assert.Contains(t, args, "--net pasta --mtu 65520")
		assert.Contains(t, args, "--port-driver=implicit", "rootlesskit refuses pasta with any port driver but its own")
	})

	t.Run("XDG directories defaulted under HOME", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "run"))
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		assert.Contains(t, startWithStandIn(t, "pasta"), "--net pasta --mtu 65520")
	})

	t.Run("a driver with no MTU of its own gets the ordinary one", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "run"))
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
		args := startWithStandIn(t, "vpnkit")
		assert.Contains(t, args, "--net vpnkit --mtu 1500")
		assert.Contains(t, args, "--port-driver=builtin")
	})

	t.Run("runtime directory defaulted to the user's", func(t *testing.T) {
		runtime := filepath.Join("/run", "user", strconv.Itoa(os.Getuid()))
		if _, err := os.Stat(runtime); err != nil {
			t.Skip("no per-user runtime directory here")
		}
		// The start makes its state directories there: whichever did not exist
		// before the test goes with it.
		for _, made := range []string{filepath.Join(runtime, "tau"), filepath.Join(runtime, "tau-containerd-rootless")} {
			if _, err := os.Stat(made); os.IsNotExist(err) {
				t.Cleanup(func() { os.RemoveAll(made) })
			}
		}

		home := t.TempDir()
		t.Setenv("XDG_RUNTIME_DIR", "")
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
		assert.Contains(t, startWithStandIn(t, "pasta"), "--state-dir "+filepath.Join(runtime, "tau-containerd-rootless"))
	})
}

// startWithStandIn starts the daemon with the stand-in rootlesskit and the given
// network driver, checks what it left behind, and returns the arguments the
// stand-in was started with.
func startWithStandIn(t *testing.T, driver string) string {
	bin := fakeBin(t, map[string]string{driver: "", "rootlesskit": `echo "$@" > "${0%/*}/args"; exec /bin/sleep 30`})
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

	return string(args)
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
		socket := filepath.Join(socketDir(t), "containerd.sock")
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

	t.Run("a socket that answers but is not containerd", func(t *testing.T) {
		socket := filepath.Join(socketDir(t), "containerd.sock")
		ln, err := net.Listen("unix", socket)
		require.NoError(t, err)
		serveStandIn(t, ln, false, false)

		_, err = New(core.ContainerdConfig{RootlessMode: core.RootlessModeDisabled, SocketPath: socket})
		assert.ErrorContains(t, err, "failed to connect to containerd at "+socket)
	})

	t.Run("a daemon that does not report its version", func(t *testing.T) {
		socket := filepath.Join(socketDir(t), "containerd.sock")
		ln, err := net.Listen("unix", socket)
		require.NoError(t, err)
		conns := &countingListener{Listener: ln}
		serveStandIn(t, conns, true, false)

		_, err = New(core.ContainerdConfig{RootlessMode: core.RootlessModeDisabled, SocketPath: socket})
		assert.ErrorContains(t, err, "failed to get containerd version")
		assert.Eventually(t, func() bool { return conns.open.Load() == 0 }, 5*time.Second, 10*time.Millisecond,
			"a daemon New refused must not keep a connection open")
	})
}

// standInNamespaces and standInVersion answer the two calls a client makes of
// containerd as it connects: the labels of its namespace, then the version.
// They are containerd's own service definitions, so New runs as it would
// against the real daemon.
type standInNamespaces struct {
	namespacesapi.UnimplementedNamespacesServer
}

func (standInNamespaces) Get(_ context.Context, req *namespacesapi.GetNamespaceRequest) (*namespacesapi.GetNamespaceResponse, error) {
	return &namespacesapi.GetNamespaceResponse{Namespace: &namespacesapi.Namespace{Name: req.Name}}, nil
}

type standInVersion struct {
	versionapi.UnimplementedVersionServer
}

func (standInVersion) Version(context.Context, *emptypb.Empty) (*versionapi.VersionResponse, error) {
	return &versionapi.VersionResponse{Version: "stand-in"}, nil
}

// serveStandIn answers on ln as containerd does, or only in part: a service
// left out is unimplemented.
func serveStandIn(t *testing.T, ln net.Listener, withNamespaces, withVersion bool) {
	t.Helper()
	server := grpc.NewServer()
	if withNamespaces {
		namespacesapi.RegisterNamespacesServer(server, standInNamespaces{})
	}
	if withVersion {
		versionapi.RegisterVersionServer(server, standInVersion{})
	}
	go server.Serve(ln)
	t.Cleanup(server.Stop)
}

// countingListener counts the connections it accepted that are still open.
type countingListener struct {
	net.Listener
	open atomic.Int32
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.open.Add(1)
	return &countedConn{Conn: conn, closed: func() { l.open.Add(-1) }}, nil
}

type countedConn struct {
	net.Conn
	once   sync.Once
	closed func()
}

func (c *countedConn) Close() error {
	c.once.Do(c.closed)
	return c.Conn.Close()
}

// TestNewStartsARootlessDaemon is the path a rootless node takes when nothing
// answers yet: New starts the daemon itself, then connects to it. rootlesskit
// is a stand-in that only sleeps, and the test plays containerd on the socket
// once the daemon has recorded the process, which is what Start waits for.
func TestNewStartsARootlessDaemon(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("rootless mode is refused to root")
	}

	bin := fakeBin(t, map[string]string{"pasta": "", "rootlesskit": "exec /bin/sleep 30"})
	home := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "run"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("TMPDIR", t.TempDir())

	socket := filepath.Join(socketDir(t), "containerd.sock")
	pidFile := filepath.Join(filepath.Dir(socket), "containerd.pid")

	server := grpc.NewServer()
	namespacesapi.RegisterNamespacesServer(server, standInNamespaces{})
	versionapi.RegisterVersionServer(server, standInVersion{})
	t.Cleanup(server.Stop)

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			if _, err := os.Stat(pidFile); err == nil {
				if ln, err := net.Listen("unix", socket); err == nil {
					server.Serve(ln)
				}
				return
			}
		}
	}()

	backend, err := New(core.ContainerdConfig{
		RootlessMode:    core.RootlessModeEnabled,
		AutoStart:       true,
		SocketPath:      socket,
		ContainerdPath:  "/bin/true",
		RootlesskitPath: filepath.Join(bin, "rootlesskit"),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		backend.client.Close()
		backend.daemon.Stop(context.Background())
	})

	require.NotNil(t, backend.daemon, "a backend that started its daemon must keep it, to stop it")
	assert.NoError(t, backend.HealthCheck(context.Background()))

	process := backend.daemon.process
	require.NotNil(t, process)
	require.NoError(t, backend.daemon.Stop(context.Background()))
	assert.Error(t, process.Signal(syscall.Signal(0)), "the daemon New started is the one Stop ends")
}

func TestBuildRejectsABadContext(t *testing.T) {
	image := &containerdImage{backend: &ContainerdBackend{client: &Client{}}, name: "x"}
	bad := func() *core.DockerfileBuild {
		return &core.DockerfileBuild{Context: strings.NewReader("not a tarball")}
	}

	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	assert.ErrorContains(t, image.Build(context.Background(), bad()), "failed to unpack build context")

	left, err := os.ReadDir(tmp)
	require.NoError(t, err)
	assert.Empty(t, left, "a refused build must not leave its directory behind")

	t.Setenv("TMPDIR", filepath.Join(tmp, "missing"))
	assert.ErrorContains(t, image.Build(context.Background(), bad()), "failed to create build directory")
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

	t.Run("no home to put the data directory in", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("XDG_DATA_HOME", "")
		assert.ErrorContains(t, start(t, context.Background(), filepath.Join(bin, "rootlesskit")), "failed to get home directory")
	})

	t.Run("no home to put the config directory in", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		assert.ErrorContains(t, start(t, context.Background(), filepath.Join(bin, "rootlesskit")), "failed to get home directory")
	})

	t.Run("the socket's directory is a file", func(t *testing.T) {
		daemon := newTestDaemon(t)
		daemon.config.ContainerdPath = "/bin/true"
		daemon.config.RootlesskitPath = filepath.Join(bin, "rootlesskit")
		dir := filepath.Dir(daemon.socketPath)
		require.NoError(t, os.RemoveAll(dir))
		require.NoError(t, os.WriteFile(dir, nil, 0o644))

		assert.ErrorContains(t, daemon.Start(context.Background()), "failed to create config file")
	})

	t.Run("the config file's place is taken", func(t *testing.T) {
		daemon := newTestDaemon(t)
		daemon.config.ContainerdPath = "/bin/true"
		daemon.config.RootlesskitPath = filepath.Join(bin, "rootlesskit")
		require.NoError(t, os.Mkdir(filepath.Join(filepath.Dir(daemon.socketPath), "containerd.toml"), 0o755))

		assert.ErrorContains(t, daemon.Start(context.Background()), "failed to create config file")
	})

	t.Run("a daemon whose pid cannot be recorded is not left running", func(t *testing.T) {
		sleeper := filepath.Join(t.TempDir(), "rootlesskit")
		require.NoError(t, os.WriteFile(sleeper, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0o755))
		daemon := newTestDaemon(t)
		daemon.config.ContainerdPath = "/bin/true"
		daemon.config.RootlesskitPath = sleeper
		require.NoError(t, os.Mkdir(daemon.stateFile, 0o755))

		assert.ErrorContains(t, daemon.Start(context.Background()), "failed to save PID")

		require.NotNil(t, daemon.exitState, "Start returned before the daemon it killed was reaped")
		status := daemon.exitState.Sys().(syscall.WaitStatus)
		assert.True(t, status.Signaled() && status.Signal() == syscall.SIGKILL, "the daemon must have been killed, got %v", daemon.exitState)
	})

	// rootlesskit refusing its flags exits at once. The start waited out its
	// whole budget on the socket of a process that was gone, and said only
	// "context deadline exceeded": this is how every rootless start failed on
	// the first hosts whose only network driver was pasta.
	t.Run("a rootlesskit that exits says why, at once", func(t *testing.T) {
		refuses := filepath.Join(t.TempDir(), "rootlesskit")
		require.NoError(t, os.WriteFile(refuses, []byte("#!/bin/sh\necho 'error: network \"pasta\" requires port driver \"none\" or \"implicit\"' >&2\nexit 1\n"), 0o755))

		err := start(t, context.Background(), refuses)
		assert.ErrorContains(t, err, "exited before its socket appeared")
		assert.ErrorContains(t, err, `requires port driver "none" or "implicit"`)
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
