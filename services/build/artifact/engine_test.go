package artifact_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/services/build/artifact"
)

// hostBackend is a core.Backend that runs the container's command as a host
// process, with its volume's source as the working directory. It is how Make's
// own part of a build — what it asks the runtime for, and what it does with the
// answer — is exercised without a daemon; the docker_integration tests are what
// prove a real runtime agrees.
type hostBackend struct {
	held      bool
	pullErr   error
	createErr error

	config  *core.ContainerConfig
	exit    int
	logs    bytes.Buffer
	removed bool
}

type hostImage struct {
	core.Image
	backend *hostBackend
}

func (i hostImage) Pull(context.Context) error  { return i.backend.pullErr }
func (i hostImage) Exists(context.Context) bool { return i.backend.held }

func (b *hostBackend) BackendType() core.BackendType                { return core.BackendTypeDocker }
func (b *hostBackend) Image(string) core.Image                      { return hostImage{backend: b} }
func (b *hostBackend) HealthCheck(context.Context) error            { return nil }
func (b *hostBackend) Capabilities() core.BackendCapabilities       { return core.BackendCapabilities{} }
func (b *hostBackend) Stop(context.Context, core.ContainerID) error { return nil }
func (b *hostBackend) Wait(context.Context, core.ContainerID) error { return nil }
func (b *hostBackend) Clean(context.Context, time.Duration, string) error {
	return nil
}

func (b *hostBackend) Create(_ context.Context, config *core.ContainerConfig) (core.ContainerID, error) {
	if b.createErr != nil {
		return "", b.createErr
	}
	b.config = config
	return "host", nil
}

func (b *hostBackend) Start(ctx context.Context, _ core.ContainerID) error {
	var dir string
	for _, v := range b.config.Volumes {
		if rel, ok := strings.CutPrefix(b.config.WorkDir, v.Destination); ok && (rel == "" || rel[0] == '/') {
			dir = filepath.Join(v.Source, rel)
		}
	}
	if dir == "" {
		return errors.New("the working directory is not a mount")
	}

	cmd := exec.CommandContext(ctx, b.config.Command[0], b.config.Command[1:]...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, b.config.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		b.exit = exit.ExitCode()
	} else if err != nil {
		return err
	}
	frame(&b.logs, 1, stdout.Bytes())
	frame(&b.logs, 2, stderr.Bytes())
	return nil
}

func frame(w io.Writer, stream byte, payload []byte) {
	if len(payload) == 0 {
		return
	}
	var header [8]byte
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	w.Write(header[:])
	w.Write(payload)
}

func (b *hostBackend) Logs(context.Context, core.ContainerID) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b.logs.Bytes())), nil
}

func (b *hostBackend) Inspect(context.Context, core.ContainerID) (*core.ContainerInfo, error) {
	return &core.ContainerInfo{ID: "host", ExitCode: b.exit, Status: "exited"}, nil
}

func (b *hostBackend) Remove(context.Context, core.ContainerID) error {
	b.removed = true
	return nil
}

func runBuild(t *testing.T, backend *hostBackend, spec artifact.Spec, opts ...artifact.EngineOption) (string, string, error) {
	t.Helper()
	e, err := artifact.NewEngine(backend, opts...)
	require.NoError(t, err)

	var log strings.Builder
	product, err := e.Make(t.Context(), spec, &log)
	if err != nil {
		return "", log.String(), err
	}
	defer product.Close()
	body, err := io.ReadAll(product)
	require.NoError(t, err)
	return string(body), log.String(), nil
}

func TestMakeRunsTheBuildConfined(t *testing.T) {
	backend := &hostBackend{}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input"), []byte("hello"), 0o644))

	body, log, err := runBuild(t, backend, artifact.Spec{
		Image:     "busybox:1.36",
		Dir:       dir,
		Commands:  []string{"echo building", "echo warned >&2", "mkdir -p out", "cp input out/app.wasm"},
		Variables: map[string]string{"GREETING": "hi"},
		Output:    "out/app.wasm",
	})
	require.NoError(t, err)
	require.Equal(t, "hello", body)
	require.Contains(t, log, "building")
	require.Contains(t, log, "warned", "stderr is part of the job's log")

	c := backend.config
	require.Equal(t, "busybox:1.36", c.Image)
	require.Equal(t, "/src", c.WorkDir)
	require.Equal(t, []core.VolumeMount{{Source: dir, Destination: "/src"}}, c.Volumes)
	require.NotNil(t, c.Network)
	require.True(t, c.Network.RestrictEgress, "a build always runs with egress restricted")
	require.Contains(t, c.Env, "GREETING=hi")
	require.True(t, backend.removed)
}

func TestMakeStopsAtTheFirstFailingCommand(t *testing.T) {
	dir := t.TempDir()
	_, _, err := runBuild(t, &hostBackend{}, artifact.Spec{
		Image:    "busybox:1.36",
		Dir:      dir,
		Commands: []string{"false", "echo ran > out"},
		Output:   "out",
	})
	require.ErrorIs(t, err, containers.ErrorExitCode)
	require.NoFileExists(t, filepath.Join(dir, "out"))
}

func TestMakeLogsTheOutputOfAFailingBuild(t *testing.T) {
	_, log, err := runBuild(t, &hostBackend{}, artifact.Spec{
		Image:    "busybox:1.36",
		Dir:      t.TempDir(),
		Commands: []string{"echo compiling", "echo main.go:3: undefined: x >&2", "false"},
		Output:   "out",
	})
	require.ErrorIs(t, err, containers.ErrorExitCode)
	require.Contains(t, log, "compiling")
	require.Contains(t, log, "main.go:3: undefined: x")
}

func TestMakeWithNoVariablesSetsNoEnvironment(t *testing.T) {
	backend := &hostBackend{}
	body, _, err := runBuild(t, backend, artifact.Spec{
		Image:    "busybox:1.36",
		Dir:      t.TempDir(),
		Commands: []string{"printf built > out"},
		Output:   "out",
	})
	require.NoError(t, err)
	require.Equal(t, "built", body)
	require.Empty(t, backend.config.Env)
}

func TestMakeReportsAMissingProduct(t *testing.T) {
	_, _, err := runBuild(t, &hostBackend{}, artifact.Spec{
		Image:    "busybox:1.36",
		Dir:      t.TempDir(),
		Commands: []string{"true"},
		Output:   "target/app.wasm",
	})
	require.ErrorContains(t, err, "left nothing at")
}

func TestMakeFailsWhenTheImageCannotBePulled(t *testing.T) {
	_, _, err := runBuild(t, &hostBackend{pullErr: errors.New("registry down")}, artifact.Spec{
		Image: "busybox:1.36", Dir: t.TempDir(), Commands: []string{"true"}, Output: "out",
	})
	require.ErrorContains(t, err, "registry down")
}

func TestMakeFailsWhenTheContainerCannotBeCreated(t *testing.T) {
	_, _, err := runBuild(t, &hostBackend{createErr: errors.New("no egress firewall")}, artifact.Spec{
		Image: "busybox:1.36", Dir: t.TempDir(), Commands: []string{"true"}, Output: "out",
	})
	require.ErrorContains(t, err, "no egress firewall")
}

// A MONOREPO RESOURCE REACHES ITS SIBLINGS. The whole checkout is mounted and
// the commands run in the resource's own directory, so a `../` dependency is
// there, and the output is read relative to that directory.
func TestMakeRunsInTheResourcesDirectoryOfTheWholeCheckout(t *testing.T) {
	backend := &hostBackend{}
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "services", "hello"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "wit"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "wit", "world.wit"), []byte("shared"), 0o644))

	body, _, err := runBuild(t, backend, artifact.Spec{
		Image:    "busybox:1.36",
		Dir:      dir,
		Path:     "services/hello",
		Commands: []string{"mkdir -p out", "cp ../../wit/world.wit out/app.wasm"},
		Output:   "out/app.wasm",
	})
	require.NoError(t, err)
	require.Equal(t, "shared", body)
	require.Equal(t, "/src/services/hello", backend.config.WorkDir)
	require.Equal(t, []core.VolumeMount{{Source: dir, Destination: "/src"}}, backend.config.Volumes)
}

// A PATH THAT CLIMBS OUT RUNS AT THE CHECKOUT ROOT, which is joinWithin's rule
// for a path carried into the container.
func TestMakeKeepsAClimbingPathInsideTheCheckout(t *testing.T) {
	backend := &hostBackend{}
	_, _, err := runBuild(t, backend, artifact.Spec{
		Image: "busybox:1.36", Dir: t.TempDir(), Path: "../../etc", Commands: []string{"printf x > out"}, Output: "out",
	})
	require.NoError(t, err)
	require.Equal(t, "/src", backend.config.WorkDir)
}

// A SECRET REACHES THE BUILD the way a variable does.
func TestMakeHandsTheBuildItsSecrets(t *testing.T) {
	backend := &hostBackend{}
	body, _, err := runBuild(t, backend, artifact.Spec{
		Image:     "busybox:1.36",
		Dir:       t.TempDir(),
		Commands:  []string{"printf %s-%s \"$GREETING\" \"$TOKEN\" > out"},
		Variables: map[string]string{"GREETING": "hi"},
		Secrets:   map[string]string{"TOKEN": "ghp_x"},
		Output:    "out",
	})
	require.NoError(t, err)
	require.Equal(t, "hi-ghp_x", body)
}

// ON A DEVELOPER'S MACHINE the build reaches what the machine reaches.
func TestMakeWithOpenEgressInstallsNoRestriction(t *testing.T) {
	backend := &hostBackend{}
	_, _, err := runBuild(t, backend, artifact.Spec{
		Image: "busybox:1.36", Dir: t.TempDir(), Commands: []string{"printf x > out"}, Output: "out",
	}, artifact.OpenEgress())
	require.NoError(t, err)
	require.False(t, backend.config.Network != nil && backend.config.Network.RestrictEgress)
}

// A TOOLCHAIN THE MACHINE ALREADY HOLDS is used when no registry has it: an
// image built here, or a registry that is down.
func TestMakeUsesAHeldImageWhenThePullFails(t *testing.T) {
	body, _, err := runBuild(t, &hostBackend{held: true, pullErr: errors.New("not in any registry")}, artifact.Spec{
		Image: "tau-local/toolchain:1", Dir: t.TempDir(), Commands: []string{"printf built > out"}, Output: "out",
	})
	require.NoError(t, err)
	require.Equal(t, "built", body)
}

// THE DIGEST NAMES THE RECIPE: the same build is one name wherever it was
// planned, a change to what runs is another, and a secret's value is no part
// of it.
func TestARecipesDigest(t *testing.T) {
	base := artifact.Spec{
		Image: "rust:1.90", Dir: "/one/machine", Path: "services/hello",
		Commands: []string{"cargo build"}, Variables: map[string]string{"A": "1"},
		Secrets: map[string]string{"TOKEN": "first"}, Output: "out.wasm",
	}
	same := base
	same.Dir = "/another/machine"
	same.Secrets = map[string]string{"TOKEN": "second"}
	require.Equal(t, base.Digest(), same.Digest(), "where the checkout is and what a secret holds are not the recipe")
	require.Len(t, base.Digest(), 64)

	for name, change := range map[string]func(*artifact.Spec){
		"image":    func(s *artifact.Spec) { s.Image = "rust:1.91" },
		"path":     func(s *artifact.Spec) { s.Path = "services/other" },
		"commands": func(s *artifact.Spec) { s.Commands = []string{"cargo build --release"} },
		"variable": func(s *artifact.Spec) { s.Variables = map[string]string{"A": "2"} },
		"secret":   func(s *artifact.Spec) { s.Secrets = map[string]string{"OTHER": "first"} },
		"output":   func(s *artifact.Spec) { s.Output = "other.wasm" },
	} {
		changed := base
		change(&changed)
		require.NotEqual(t, base.Digest(), changed.Digest(), "a different %s is a different recipe", name)
	}
}
