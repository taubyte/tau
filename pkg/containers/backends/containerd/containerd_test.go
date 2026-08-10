//go:build linux

// Unit tests for the containerd backend. Everything here runs without a
// containerd daemon, without docker and without the network: the tests that
// need a live runtime live in containerd_integration_test.go behind the
// containerd_integration tag.

package containerd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

func TestDetectRootlessMode(t *testing.T) {
	t.Run("explicit modes are preserved", func(t *testing.T) {
		for _, mode := range []core.RootlessMode{core.RootlessModeEnabled, core.RootlessModeDisabled} {
			backend := &ContainerdBackend{config: core.ContainerdConfig{RootlessMode: mode}}

			// Enabling rootless mode as root is a conflict, and this suite is
			// not run as root; every other combination must be left alone.
			if mode == core.RootlessModeEnabled && os.Geteuid() == 0 {
				assert.Error(t, backend.detectRootlessMode(), "rootless mode as root must be refused")
				continue
			}

			require.NoError(t, backend.detectRootlessMode())
			assert.Equal(t, mode, backend.config.RootlessMode, "explicit mode %v must survive detection", mode)
		}
	})

	t.Run("auto resolves by euid", func(t *testing.T) {
		backend := &ContainerdBackend{config: core.ContainerdConfig{RootlessMode: core.RootlessModeAuto}}

		require.NoError(t, backend.detectRootlessMode())

		want := core.RootlessModeEnabled
		if os.Geteuid() == 0 {
			want = core.RootlessModeDisabled
		}
		assert.Equal(t, want, backend.config.RootlessMode)
		assert.Equal(t, want == core.RootlessModeEnabled, backend.isRootlessMode())
	})
}

func TestGetSocketPath(t *testing.T) {
	t.Run("explicit path wins", func(t *testing.T) {
		backend := &ContainerdBackend{config: core.ContainerdConfig{
			SocketPath:   "/tmp/explicit.sock",
			RootlessMode: core.RootlessModeEnabled,
		}}

		path, err := backend.getSocketPath()
		require.NoError(t, err)
		assert.Equal(t, "/tmp/explicit.sock", path)
	})

	t.Run("rootful uses the system socket", func(t *testing.T) {
		backend := &ContainerdBackend{config: core.ContainerdConfig{RootlessMode: core.RootlessModeDisabled}}

		path, err := backend.getSocketPath()
		require.NoError(t, err)
		assert.Equal(t, "/run/containerd/containerd.sock", path)
	})

	t.Run("rootless uses a per-user socket", func(t *testing.T) {
		backend := &ContainerdBackend{config: core.ContainerdConfig{RootlessMode: core.RootlessModeEnabled}}

		path, err := backend.getSocketPath()
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(path, "/run/user/"), "got %s", path)
		assert.True(t, strings.HasSuffix(path, "/tau/containerd/containerd.sock"), "got %s", path)
	})
}

func TestCapabilities(t *testing.T) {
	// Builds run BuildKit in a container, so this backend can build wherever it
	// can run. Callers route Dockerfile builds by this flag.
	assert.True(t, (&ContainerdBackend{}).Capabilities().SupportsBuild)
}

func TestEnsureContainerdRunningWithoutSocket(t *testing.T) {
	t.Run("rootful reports the system daemon is missing", func(t *testing.T) {
		backend := &ContainerdBackend{config: core.ContainerdConfig{
			RootlessMode: core.RootlessModeDisabled,
			SocketPath:   filepath.Join(t.TempDir(), "containerd.sock"),
		}}

		err := backend.ensureContainerdRunning(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "system-wide")
	})

	t.Run("rootless without autostart says so", func(t *testing.T) {
		backend := &ContainerdBackend{config: core.ContainerdConfig{
			RootlessMode: core.RootlessModeEnabled,
			AutoStart:    false,
			SocketPath:   filepath.Join(t.TempDir(), "containerd.sock"),
		}}

		err := backend.ensureContainerdRunning(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "AutoStart is disabled")
	})
}

func TestTestSocketConnection(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "containerd.sock")
	backend := &ContainerdBackend{config: core.ContainerdConfig{SocketPath: socket}}

	err := backend.testSocketConnection()
	require.Error(t, err, "a missing socket must not look connectable")
	assert.Contains(t, err.Error(), "does not exist")

	// A plain file at the socket path exists but does not accept connections.
	require.NoError(t, os.WriteFile(socket, nil, 0644))
	err = backend.testSocketConnection()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to connect to socket")

	require.NoError(t, os.Remove(socket))
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	defer listener.Close()

	assert.NoError(t, backend.testSocketConnection(), "a live socket must connect")
}

func TestSpecOpts(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		spec := applySpecOpts(t, &core.ContainerConfig{Command: []string{"echo", "hi"}})

		assert.Equal(t, []string{"echo", "hi"}, spec.Process.Args)
		assert.NotContains(t, namespaceTypes(spec), specs.NetworkNamespace,
			"the default must not unshare the network: there is no CNI to give the container a route out")
	})

	t.Run("env and workdir", func(t *testing.T) {
		spec := applySpecOpts(t, &core.ContainerConfig{
			Env:     []string{"FOO=bar"},
			WorkDir: "/work",
		})

		assert.Equal(t, "/work", spec.Process.Cwd)
		assert.Contains(t, spec.Process.Env, "FOO=bar")
	})

	t.Run("env replaces by key rather than duplicating", func(t *testing.T) {
		// An image ships a PATH; a build overriding it must end up with one
		// PATH, not two whose precedence depends on the runtime.
		spec := &specs.Spec{Process: &specs.Process{Env: []string{"PATH=/image/bin", "KEEP=1"}}}

		opts, err := specOpts(&core.ContainerConfig{Env: []string{"PATH=/build/bin"}})
		require.NoError(t, err)
		applyTo(t, spec, opts)

		assert.Contains(t, spec.Process.Env, "PATH=/build/bin")
		assert.NotContains(t, spec.Process.Env, "PATH=/image/bin")
		assert.Contains(t, spec.Process.Env, "KEEP=1", "unrelated image variables must survive")
	})

	t.Run("volumes become bind mounts", func(t *testing.T) {
		spec := applySpecOpts(t, &core.ContainerConfig{
			Volumes: []core.VolumeMount{
				{Source: "/host/src", Destination: "/src"},
				{Source: "/host/ro", Destination: "/ro", ReadOnly: true},
			},
		})

		src := findMount(t, spec, "/src")
		assert.Equal(t, "bind", src.Type)
		assert.Equal(t, "/host/src", src.Source)
		assert.Contains(t, src.Options, "rbind")
		assert.Contains(t, src.Options, "rw")

		ro := findMount(t, spec, "/ro")
		assert.Contains(t, ro.Options, "ro", "a read-only volume must be mounted read-only")
	})

	t.Run("named volumes are refused", func(t *testing.T) {
		_, err := specOpts(&core.ContainerConfig{
			Volumes: []core.VolumeMount{{Source: "myvol", Destination: "/data", IsVolumeName: true}},
		})
		require.Error(t, err, "containerd has no volume manager: silently mounting nothing would be worse")
		assert.Contains(t, err.Error(), "not supported")
	})

	t.Run("network modes", func(t *testing.T) {
		none := applySpecOpts(t, &core.ContainerConfig{Network: &core.NetworkConfig{Mode: "none"}})
		assert.Contains(t, namespaceTypes(none), specs.NetworkNamespace, "none must unshare the network")

		host := applySpecOpts(t, &core.ContainerConfig{Network: &core.NetworkConfig{Mode: "host"}})
		assert.NotContains(t, namespaceTypes(host), specs.NetworkNamespace)

		_, err := specOpts(&core.ContainerConfig{Network: &core.NetworkConfig{Mode: "bridge"}})
		require.Error(t, err, "bridge needs CNI, which is not wired up: it must fail loudly")

		_, err = specOpts(&core.ContainerConfig{Network: &core.NetworkConfig{
			PortMappings: []core.PortMapping{{HostPort: 8080, ContainerPort: 80}},
		}})
		require.Error(t, err, "port mappings are meaningless on host networking and must not be accepted silently")
	})

	t.Run("resource limits", func(t *testing.T) {
		spec := applySpecOpts(t, &core.ContainerConfig{
			Resources: &core.ResourceLimits{Memory: 512 << 20, PIDs: 128, CPUQuota: 50000},
		})

		require.NotNil(t, spec.Linux.Resources.Memory)
		assert.Equal(t, int64(512<<20), *spec.Linux.Resources.Memory.Limit)
		require.NotNil(t, spec.Linux.Resources.Pids)
		assert.Equal(t, int64(128), spec.Linux.Resources.Pids.Limit)
		require.NotNil(t, spec.Linux.Resources.CPU)
		assert.Equal(t, int64(50000), *spec.Linux.Resources.CPU.Quota)
		assert.Equal(t, uint64(100000), *spec.Linux.Resources.CPU.Period, "a quota without a period is not a limit")
	})
}

// applySpecOpts builds the tau options for a config and applies them to
// containerd's default spec, which is what Create layers them onto.
func applySpecOpts(t *testing.T, config *core.ContainerConfig) *specs.Spec {
	t.Helper()

	opts, err := specOpts(config)
	require.NoError(t, err)

	spec := &specs.Spec{}
	ctx := namespaces.WithNamespace(context.Background(), "tau-test")
	require.NoError(t, oci.WithDefaultSpec()(ctx, nil, &containers.Container{ID: "test"}, spec))

	applyTo(t, spec, opts)

	return spec
}

func applyTo(t *testing.T, spec *specs.Spec, opts []oci.SpecOpts) {
	t.Helper()

	ctx := namespaces.WithNamespace(context.Background(), "tau-test")
	for _, opt := range opts {
		require.NoError(t, opt(ctx, nil, &containers.Container{ID: "test"}, spec))
	}
}

func namespaceTypes(spec *specs.Spec) []specs.LinuxNamespaceType {
	var types []specs.LinuxNamespaceType
	for _, ns := range spec.Linux.Namespaces {
		types = append(types, ns.Type)
	}
	return types
}

func findMount(t *testing.T, spec *specs.Spec, destination string) specs.Mount {
	t.Helper()
	for _, m := range spec.Mounts {
		if m.Destination == destination {
			return m
		}
	}
	t.Fatalf("no mount at %s in %+v", destination, spec.Mounts)
	return specs.Mount{}
}

// containerd hands out stdout and stderr as two plain streams with no log store
// behind them, so the backend drains them as the container runs and frames the
// result the way stdcopy expects. These tests drive that path with in-memory
// streams — no daemon involved.
func TestLogs(t *testing.T) {
	newBackend := func(stdout, stderr io.ReadCloser) *ContainerdBackend {
		tio := &taskIO{stdout: stdout, stderr: stderr}
		tio.drain()
		return &ContainerdBackend{
			client: &Client{},
			tasks:  map[core.ContainerID]*taskIO{"c": tio},
		}
	}

	readAll := func(t *testing.T, backend *ContainerdBackend) (string, string) {
		t.Helper()
		logs, err := backend.Logs(context.Background(), "c")
		require.NoError(t, err)
		defer logs.Close()

		var out, errOut bytes.Buffer
		_, err = stdcopy.StdCopy(&out, &errOut, logs)
		require.NoError(t, err, "output must decode as a docker-multiplexed stream")
		return out.String(), errOut.String()
	}

	t.Run("streams stay separated", func(t *testing.T) {
		backend := newBackend(
			io.NopCloser(strings.NewReader("to stdout")),
			io.NopCloser(strings.NewReader("to stderr")),
		)

		out, errOut := readAll(t, backend)
		assert.Equal(t, "to stdout", out)
		assert.Equal(t, "to stderr", errOut)
	})

	t.Run("output larger than one copy buffer survives", func(t *testing.T) {
		// io.Copy works in 32KiB chunks, so this crosses many frames and
		// interleaves the two streams in the collected output.
		payload := strings.Repeat("0123456789abcdef", 64*1024) // 1 MiB
		backend := newBackend(
			io.NopCloser(strings.NewReader(payload)),
			io.NopCloser(strings.NewReader(payload)),
		)

		out, errOut := readAll(t, backend)
		assert.Equal(t, len(payload), len(out), "stdout must not be truncated")
		assert.Equal(t, len(payload), len(errOut), "stderr must not be truncated")
		assert.Equal(t, payload, out)
	})

	t.Run("a chatty container is not blocked waiting to be read", func(t *testing.T) {
		// The regression that matters: a FIFO is a pipe, so a container writing
		// more than its buffer holds blocks until something reads. Collecting
		// only when Logs is called deadlocks every build that talks a lot.
		// Nothing calls Logs here — the writes must still complete.
		stdout, containerStdout := io.Pipe()
		stderr, containerStderr := io.Pipe()

		tio := &taskIO{stdout: stdout, stderr: stderr}
		tio.drain()

		written := make(chan error, 1)
		go func() {
			// Far more than any pipe buffer.
			_, err := containerStdout.Write(bytes.Repeat([]byte("x"), 4<<20))
			written <- err
		}()

		select {
		case err := <-written:
			require.NoError(t, err, "the container's writes must go through")
		case <-time.After(10 * time.Second):
			t.Fatal("container blocked writing output: nothing is draining the FIFO")
		}

		containerStdout.Close()
		containerStderr.Close()

		collected, err := tio.collected(context.Background())
		require.NoError(t, err)
		assert.NotEmpty(t, collected)
	})

	t.Run("collecting respects context", func(t *testing.T) {
		// A container still running has streams that have not ended; the caller
		// must get its context back rather than hang.
		stdout, open := io.Pipe()
		defer open.Close()

		tio := &taskIO{stdout: stdout, stderr: io.NopCloser(strings.NewReader(""))}
		tio.drain()

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, err := tio.collected(ctx)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("unknown container", func(t *testing.T) {
		backend := &ContainerdBackend{client: &Client{}, tasks: map[core.ContainerID]*taskIO{}}

		_, err := backend.Logs(context.Background(), "missing")
		assert.Error(t, err)
	})
}

func TestStreamFramerConcurrentWriters(t *testing.T) {
	// Both streams write into one pipe: a frame header must never be split
	// from its payload by the other stream.
	var buf bytes.Buffer
	var mu sync.Mutex

	var wg sync.WaitGroup
	for _, stream := range []byte{streamStdout, streamStderr} {
		wg.Add(1)
		go func(stream byte) {
			defer wg.Done()
			framer := &streamFramer{mu: &mu, w: &buf, stream: stream}
			for i := 0; i < 200; i++ {
				fmt.Fprintf(framer, "stream-%d-line-%d\n", stream, i)
			}
		}(stream)
	}
	wg.Wait()

	var out, errOut bytes.Buffer
	_, err := stdcopy.StdCopy(&out, &errOut, &buf)
	require.NoError(t, err, "interleaved frames must still decode")
	assert.Equal(t, 200, strings.Count(out.String(), "\n"))
	assert.Equal(t, 200, strings.Count(errOut.String(), "\n"))
}

func TestOperationsWithoutClient(t *testing.T) {
	// Every entry point must refuse rather than panic when the backend was
	// never connected.
	backend := &ContainerdBackend{}
	ctx := context.Background()

	_, err := backend.Create(ctx, &core.ContainerConfig{Image: "alpine"})
	assert.Error(t, err)
	assert.Error(t, backend.Start(ctx, "c"))
	assert.Error(t, backend.Stop(ctx, "c"))
	assert.Error(t, backend.Remove(ctx, "c"))
	assert.Error(t, backend.Wait(ctx, "c"))
	assert.Error(t, backend.HealthCheck(ctx))

	_, err = backend.Logs(ctx, "c")
	assert.Error(t, err)

	_, err = backend.Inspect(ctx, "c")
	assert.Error(t, err)
}

func TestImageWithoutClient(t *testing.T) {
	image := (&ContainerdBackend{}).Image("alpine:latest")

	assert.Equal(t, "alpine:latest", image.Name())
	assert.False(t, image.Exists(context.Background()), "an unconnected backend cannot have images")
	assert.Error(t, image.Pull(context.Background()))
	assert.Error(t, image.Remove(context.Background()))

	_, err := image.Digest(context.Background())
	assert.Error(t, err)

	_, err = image.Tags(context.Background())
	assert.Error(t, err)
}

func TestTaskIOCloseIsIdempotent(t *testing.T) {
	// close runs on error paths, on Stop and on Remove; a double close must not
	// panic and must still remove the FIFO directory.
	dir := t.TempDir()
	fifoDir := filepath.Join(dir, "fifos")
	require.NoError(t, os.MkdirAll(fifoDir, 0755))

	tio := &taskIO{fifoDir: fifoDir}
	tio.close()
	tio.close()

	_, err := os.Stat(fifoDir)
	assert.True(t, os.IsNotExist(err), "the FIFO directory must be removed, not leaked")
}

// Every containerd call needs a namespace, and the egress firewall names the
// cgroup it matches after it, so a backend built without one must not end up
// with an empty string.
func TestNamespaceDefaulted(t *testing.T) {
	assert.Equal(t, defaultNamespace, withDefaults(core.ContainerdConfig{}).Namespace,
		"a config literal with no namespace is what the backend manager builds")

	assert.Equal(t, "chosen", withDefaults(core.ContainerdConfig{Namespace: "chosen"}).Namespace,
		"a caller that names one keeps it")

	assert.NotContains(t, restrictedCgroupPath(withDefaults(core.ContainerdConfig{}).Namespace, "id"), "//",
		"the restricted cgroup path must not have an empty component")
}
