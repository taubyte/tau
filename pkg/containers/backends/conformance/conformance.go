// Package conformance holds the behaviour every container backend must
// exhibit. Docker and containerd are interchangeable only if they agree on
// what a run does — exit codes, log content and framing, environment, working
// directory, bind mounts, cleanup — so both backends run this one suite
// instead of each carrying its own hand-written and quietly divergent set.
//
// The suite is driven from each backend's integration tests, which already
// gate on their runtime being present.
package conformance

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

// Backend is what a suite runs against: a live backend and the name of a
// shell-carrying image it can run, spelled the way that backend expects
// (containerd needs a fully qualified reference, docker does not).
type Backend struct {
	Backend core.Backend
	Image   string
}

// Run executes the whole suite. Each case is a subtest, so a backend that
// fails one still reports the rest.
func Run(t *testing.T, b Backend) {
	t.Helper()

	require.NotNil(t, b.Backend, "backend is required")
	require.NotEmpty(t, b.Image, "image is required")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	require.NoError(t, b.Backend.Image(b.Image).Pull(ctx), "pulling %s must succeed", b.Image)

	t.Run("Stdout", func(t *testing.T) { testStdout(t, b) })
	t.Run("Stderr", func(t *testing.T) { testStderr(t, b) })
	t.Run("ExitCode", func(t *testing.T) { testExitCode(t, b) })
	t.Run("Env", func(t *testing.T) { testEnv(t, b) })
	t.Run("WorkDir", func(t *testing.T) { testWorkDir(t, b) })
	t.Run("BindMount", func(t *testing.T) { testBindMount(t, b) })
	t.Run("ImageDefaults", func(t *testing.T) { testImageDefaults(t, b) })
	t.Run("LargeOutput", func(t *testing.T) { testLargeOutput(t, b) })
	t.Run("RemoveIsFinal", func(t *testing.T) { testRemoveIsFinal(t, b) })
	t.Run("CleanKeepsRecentImages", func(t *testing.T) { testCleanKeepsRecentImages(t, b) })
	t.Run("HealthCheck", func(t *testing.T) {
		assert.NoError(t, b.Backend.HealthCheck(context.Background()))
	})
}

// result is what one container run produced.
type result struct {
	stdout   string
	stderr   string
	exitCode int
}

// run creates, starts and waits for a container, collects its output and
// removes it. It mirrors what containers.Container.Run does, including reading
// the logs before removal.
func run(t *testing.T, b Backend, config *core.ContainerConfig) result {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	config.Image = b.Image

	id, err := b.Backend.Create(ctx, config)
	require.NoError(t, err, "create must succeed")

	// Removal is registered before start so a failing assertion cannot leak
	// the container onto the host.
	t.Cleanup(func() {
		removeCtx, removeCancel := context.WithTimeout(context.Background(), time.Minute)
		defer removeCancel()
		b.Backend.Remove(removeCtx, id)
	})

	require.NoError(t, b.Backend.Start(ctx, id), "start must succeed")

	// A non-zero exit is the container's result, not a wait failure.
	require.NoError(t, b.Backend.Wait(ctx, id), "wait must succeed whatever the container exits with")

	info, err := b.Backend.Inspect(ctx, id)
	require.NoError(t, err, "inspect must succeed")

	logs, err := b.Backend.Logs(ctx, id)
	require.NoError(t, err, "logs must succeed")
	defer logs.Close()

	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, logs)
	require.NoError(t, err, "backend logs must be a docker-multiplexed stream")

	require.NoError(t, b.Backend.Remove(ctx, id), "remove must succeed")

	return result{stdout: stdout.String(), stderr: stderr.String(), exitCode: info.ExitCode}
}

// sh runs a shell snippet in the container.
func sh(script string) *core.ContainerConfig {
	return &core.ContainerConfig{Command: []string{"/bin/sh", "-c", script}}
}

func testStdout(t *testing.T, b Backend) {
	got := run(t, b, sh("echo hello stdout"))

	assert.Contains(t, got.stdout, "hello stdout", "stdout must reach the caller")
	assert.NotContains(t, got.stderr, "hello stdout", "stdout must not be reported as stderr")
	assert.Equal(t, 0, got.exitCode)
}

func testStderr(t *testing.T, b Backend) {
	got := run(t, b, sh("echo hello stderr >&2"))

	assert.Contains(t, got.stderr, "hello stderr", "stderr must reach the caller on the stderr stream")
	assert.NotContains(t, got.stdout, "hello stderr", "stderr must not be reported as stdout")
}

func testExitCode(t *testing.T, b Backend) {
	// A build step that fails must surface its status and its output: the
	// output is the compiler error the user needs to see.
	got := run(t, b, sh("echo failing output; exit 3"))

	assert.Equal(t, 3, got.exitCode, "inspect must report the container's exit code")
	assert.Contains(t, got.stdout, "failing output", "output of a failed container must still be readable")
}

func testEnv(t *testing.T, b Backend) {
	config := sh("echo \"$TAU_TEST_VAR\"")
	config.Env = []string{"TAU_TEST_VAR=from-env"}

	got := run(t, b, config)

	assert.Contains(t, got.stdout, "from-env", "environment variables must be passed through")
}

func testWorkDir(t *testing.T, b Backend) {
	config := sh("pwd")
	config.WorkDir = "/tmp"

	got := run(t, b, config)

	assert.Contains(t, got.stdout, "/tmp", "working directory must be honoured")
}

func testBindMount(t *testing.T, b Backend) {
	// The builder's whole job is mounting a source tree in and reading build
	// output back out, so both directions are checked.
	dir := t.TempDir()
	// The container runs as root and must be able to write here.
	require.NoError(t, os.Chmod(dir, 0777))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "in.txt"), []byte("mounted-in"), 0644))

	config := sh("cat /mnt/in.txt && echo mounted-out > /mnt/out.txt")
	config.Volumes = []core.VolumeMount{{Source: dir, Destination: "/mnt"}}

	got := run(t, b, config)

	assert.Equal(t, 0, got.exitCode, "mounted container must succeed, stderr: %s", got.stderr)
	assert.Contains(t, got.stdout, "mounted-in", "a bind-mounted file must be readable in the container")

	out, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	require.NoError(t, err, "a file written to the mount must appear on the host")
	assert.Equal(t, "mounted-out\n", string(out))
}

func testImageDefaults(t *testing.T, b Backend) {
	// With no command of its own, a container runs what the image says to run.
	// A backend that ignores the image's configuration has nothing to start —
	// and would equally be ignoring the image's environment, which is where a
	// build image puts the toolchain on PATH.
	got := run(t, b, &core.ContainerConfig{})

	assert.Equal(t, 0, got.exitCode, "the image's own entrypoint must run, stderr: %s", got.stderr)
}

func testLargeOutput(t *testing.T, b Backend) {
	// Output larger than a pipe buffer and than one stream frame: catches
	// truncation from removing the container while its logs are still being
	// read, and framing that assumes a single write.
	const lines = 20000
	got := run(t, b, sh(fmt.Sprintf("i=0; while [ $i -lt %d ]; do echo line-$i; i=$((i+1)); done", lines)))

	assert.Equal(t, lines, strings.Count(got.stdout, "\n"), "no output may be lost")
	assert.Contains(t, got.stdout, fmt.Sprintf("line-%d\n", lines-1), "the last line must be present")
}

func testRemoveIsFinal(t *testing.T, b Backend) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	id, err := b.Backend.Create(ctx, &core.ContainerConfig{
		Image:   b.Image,
		Command: []string{"/bin/sh", "-c", "echo done"},
	})
	require.NoError(t, err)

	require.NoError(t, b.Backend.Start(ctx, id))
	require.NoError(t, b.Backend.Wait(ctx, id))

	logs, err := b.Backend.Logs(ctx, id)
	require.NoError(t, err)
	io.Copy(io.Discard, logs)
	logs.Close()

	require.NoError(t, b.Backend.Remove(ctx, id), "remove must succeed")

	_, err = b.Backend.Inspect(ctx, id)
	assert.Error(t, err, "a removed container must not be inspectable")
}

func testCleanKeepsRecentImages(t *testing.T, b Backend) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	image := b.Backend.Image(b.Image)
	require.NoError(t, image.Pull(ctx))

	// Only the harmless direction is asserted. Clean takes an age and no filter,
	// so a test that proved deletion would delete images off whatever machine
	// ran it — a century's cutoff keeps everything and still exercises the
	// listing and traversal, which is where a backend that cannot sweep fails.
	require.NoError(t, b.Backend.Clean(ctx, 100*365*24*time.Hour, b.Image), "a sweep must not error")

	assert.True(t, image.Exists(ctx), "an image younger than the cutoff must survive the sweep")
}

// RunStopsRunningContainer checks that a container still running can be stopped
// and removed, which is how a build is cancelled. It is separate from Run
// because it is the only case that leaves a container running.
func RunStopsRunningContainer(t *testing.T, b Backend) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	require.NoError(t, b.Backend.Image(b.Image).Pull(ctx))

	id, err := b.Backend.Create(ctx, &core.ContainerConfig{
		Image:   b.Image,
		Command: []string{"/bin/sh", "-c", "echo before stop; sleep 300"},
	})
	require.NoError(t, err)

	require.NoError(t, b.Backend.Start(ctx, id))
	require.NoError(t, b.Backend.Stop(ctx, id), "a running container must be stoppable")

	// A stopped container must still be distinguishable from one that was never
	// run. A backend that discards the runtime record on stop reports the zero
	// value here, which reads as a container that succeeded.
	info, err := b.Backend.Inspect(ctx, id)
	require.NoError(t, err, "a stopped container must still be inspectable")
	assert.NotEqual(t, "created", info.Status, "a container that ran and was stopped is not merely created")

	// Stopping must not throw away the output: the reason an operator stopped a
	// container is usually in its logs.
	logs, err := b.Backend.Logs(ctx, id)
	require.NoError(t, err, "logs must still be available after a stop")
	defer logs.Close()

	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, logs)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "before stop", "output produced before the stop must survive it")

	// Stopping something already stopped is how cancellation paths behave when
	// they race, and must not be an error.
	assert.NoError(t, b.Backend.Stop(ctx, id), "stopping an already-stopped container must be a no-op")

	require.NoError(t, b.Backend.Remove(ctx, id), "a stopped container must be removable")
}
