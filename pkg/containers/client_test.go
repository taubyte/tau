package containers

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

type fakeImage struct {
	name     string
	exists   bool
	pullErr  error
	buildErr error

	pulls  int
	builds []*core.DockerfileBuild
}

func (i *fakeImage) Pull(context.Context) error { i.pulls++; return i.pullErr }
func (i *fakeImage) Build(_ context.Context, in *core.DockerfileBuild) error {
	i.builds = append(i.builds, in)
	return i.buildErr
}
func (i *fakeImage) Exists(context.Context) bool            { return i.exists }
func (i *fakeImage) Remove(context.Context) error           { return nil }
func (i *fakeImage) Name() string                           { return i.name }
func (i *fakeImage) Digest(context.Context) (string, error) { return "", nil }
func (i *fakeImage) Tags(context.Context) ([]string, error) { return nil, nil }

// imageBackend is a fakeBackend that hands out one image and reports its own
// health, build support and cleaning.
type imageBackend struct {
	fakeBackend
	kind       core.BackendType
	image      *fakeImage
	noBuild    bool
	healthErr  error
	createErr  error
	removeErr  error
	cleanCalls []string
	created    *core.ContainerConfig
}

func (b *imageBackend) BackendType() core.BackendType { return b.kind }
func (b *imageBackend) Image(string) core.Image       { return b.image }
func (b *imageBackend) HealthCheck(context.Context) error {
	return b.healthErr
}
func (b *imageBackend) Capabilities() core.BackendCapabilities {
	return core.BackendCapabilities{SupportsBuild: !b.noBuild}
}
func (b *imageBackend) Create(_ context.Context, config *core.ContainerConfig) (core.ContainerID, error) {
	b.created = config
	if b.createErr != nil {
		return "", b.createErr
	}
	return "fake-id", nil
}
func (b *imageBackend) Remove(context.Context, core.ContainerID) error { return b.removeErr }
func (b *imageBackend) Clean(_ context.Context, age time.Duration, ref string) error {
	b.cleanCalls = append(b.cleanCalls, age.String()+" "+ref)
	return nil
}

func newClient(t *testing.T, b core.Backend) *Client {
	t.Helper()
	c, err := New(WithBackend(b), Verbose())
	require.NoError(t, err)
	assert.True(t, c.progressOutput)
	assert.NoError(t, c.Close())
	return c
}

func TestNewRefusesANilBackend(t *testing.T) {
	_, err := New(WithBackend(nil))
	assert.ErrorContains(t, err, "nil backend")
}

func TestImagePullsWhenNothingToBuild(t *testing.T) {
	img := &fakeImage{name: "alpine"}
	c := newClient(t, &imageBackend{image: img})

	image, err := c.Image(context.Background(), "alpine")
	require.NoError(t, err)
	assert.Equal(t, "alpine", image.Name())
	assert.Equal(t, 1, img.pulls)
	assert.Empty(t, img.builds)
}

func TestImageFallsBackToALocalCopyWhenPullFails(t *testing.T) {
	img := &fakeImage{name: "alpine", exists: true, pullErr: errors.New("offline")}
	c := newClient(t, &imageBackend{image: img})

	image, err := c.Image(context.Background(), "alpine")
	require.NoError(t, err)
	assert.True(t, image.Exists(context.Background()))
}

func TestImagePullFailureWithoutLocalCopy(t *testing.T) {
	img := &fakeImage{name: "alpine", pullErr: errors.New("offline")}
	c := newClient(t, &imageBackend{image: img})

	_, err := c.Image(context.Background(), "alpine")
	assert.ErrorIs(t, err, ErrorImagePull)
	assert.ErrorIs(t, err, ErrorClientPull)
	assert.ErrorContains(t, err, "offline")
}

func TestPullReportsStatus(t *testing.T) {
	img := &fakeImage{name: "alpine"}
	image := &DockerImage{backend: &imageBackend{image: img}, image: "alpine"}

	status := make(chan PullStatus, 1)
	_, err := image.Pull(context.Background(), status)
	require.NoError(t, err)
	assert.Equal(t, "Image pulled successfully", (<-status).Status)

	// A full channel is skipped, not blocked on.
	status <- PullStatus{}
	_, err = image.Pull(context.Background(), status)
	assert.NoError(t, err)
}

func TestImageBuildsADockerfile(t *testing.T) {
	img := &fakeImage{name: "built"}
	c := newClient(t, &imageBackend{image: img})

	path := filepath.Join(t.TempDir(), "Dockerfile")
	require.NoError(t, os.WriteFile(path, []byte("FROM alpine\n"), 0o644))

	var out bytes.Buffer
	_, err := c.Image(context.Background(), "built", Dockerfile(path), Output(&out), RestrictedBuildEgress())
	require.NoError(t, err)
	require.Len(t, img.builds, 1)
	assert.Zero(t, img.pulls)
	assert.True(t, img.builds[0].RestrictEgress)

	gz, err := gzip.NewReader(img.builds[0].Context)
	require.NoError(t, err)
	hdr, err := tar.NewReader(gz).Next()
	require.NoError(t, err)
	assert.Equal(t, "Dockerfile", hdr.Name)

	_, err = c.Image(context.Background(), "built", Dockerfile(filepath.Join(t.TempDir(), "missing")))
	assert.ErrorIs(t, err, ErrorImageOptions)
}

func TestImageSkipsBuildingAnExistingImage(t *testing.T) {
	img := &fakeImage{name: "built", exists: true}
	c := newClient(t, &imageBackend{image: img})

	_, err := c.Image(context.Background(), "built", Build(bytes.NewReader(nil)))
	require.NoError(t, err)
	assert.Empty(t, img.builds)

	_, err = c.Image(context.Background(), "built", Build(bytes.NewReader(nil)), Rebuild())
	require.NoError(t, err)
	assert.Len(t, img.builds, 1)
}

func TestImageBuildFailures(t *testing.T) {
	img := &fakeImage{name: "built", buildErr: errors.New("bad step")}
	_, err := newClient(t, &imageBackend{image: img}).
		Image(context.Background(), "built", Build(bytes.NewReader(nil)))
	assert.ErrorIs(t, err, ErrorImageBuild)
	assert.ErrorIs(t, err, ErrorImageBuildDockerFile)
	assert.ErrorContains(t, err, "bad step")

	_, err = newClient(t, &imageBackend{image: &fakeImage{}, noBuild: true}).
		Image(context.Background(), "built", Build(bytes.NewReader(nil)))
	assert.ErrorContains(t, err, "does not support building")
}

func TestImageOptionFailure(t *testing.T) {
	bad := func(*DockerImage) error { return errors.New("nope") }
	_, err := newClient(t, &imageBackend{image: &fakeImage{}}).Image(context.Background(), "x", bad)
	assert.ErrorIs(t, err, ErrorImageOptions)
}

func TestInstantiate(t *testing.T) {
	backend := &imageBackend{image: &fakeImage{}}
	image := &DockerImage{backend: backend, image: "alpine"}

	c, err := image.Instantiate(context.Background(), RestrictedEgress(), Command([]string{"true"}))
	require.NoError(t, err)
	assert.Equal(t, core.ContainerID("fake-id"), c.id)
	require.NotNil(t, backend.created.Network)
	assert.True(t, backend.created.Network.RestrictEgress)

	_, err = image.Instantiate(context.Background(), func(*Container) error { return errors.New("nope") })
	assert.ErrorIs(t, err, ErrorContainerOptions)

	backend.createErr = errors.New("no room")
	_, err = image.Instantiate(context.Background())
	assert.ErrorIs(t, err, ErrorContainerCreate)
	assert.ErrorContains(t, err, "no room")
}

func TestCleanupFailure(t *testing.T) {
	backend := &imageBackend{removeErr: errors.New("busy")}
	err := (&Container{backend: backend, id: "x"}).Cleanup(context.Background())
	assert.ErrorContains(t, err, "busy")
}

func TestClientCleanUsesTheBackend(t *testing.T) {
	backend := &imageBackend{}
	require.NoError(t, newClient(t, backend).Clean(context.Background(), time.Hour, "ref"))
	assert.Equal(t, []string{"1h0m0s ref"}, backend.cleanCalls)
}

// choosingFrom is a client that chooses its backend from f.
func choosingFrom(f factories) Option {
	return func(c *Client) error {
		c.factories = f
		return nil
	}
}

func factoryOf(b core.Backend, err error) core.BackendFactory {
	return func(core.BackendConfig) (core.Backend, error) { return b, err }
}

func TestSelectBackendPrefersDocker(t *testing.T) {
	docker := &imageBackend{kind: core.BackendTypeDocker}
	c, err := New(choosingFrom(factories{
		core.BackendTypeDocker:     factoryOf(docker, nil),
		core.BackendTypeContainerd: factoryOf(&imageBackend{kind: core.BackendTypeContainerd}, nil),
	}))
	require.NoError(t, err)
	assert.Same(t, docker, c.backend)
}

func TestSelectBackendFallsBackToContainerd(t *testing.T) {
	containerd := &imageBackend{kind: core.BackendTypeContainerd}
	f := factories{
		core.BackendTypeDocker:     factoryOf(&imageBackend{healthErr: errors.New("down")}, nil),
		core.BackendTypeContainerd: factoryOf(containerd, nil),
	}

	backend, err := f.choose()
	require.NoError(t, err)
	assert.Same(t, containerd, backend)

	// Clean on a client with no backend picks the default one too.
	require.NoError(t, (&Client{factories: f}).Clean(context.Background(), time.Minute, ""))
	assert.Equal(t, []string{"1m0s "}, containerd.cleanCalls)
}

func TestSelectBackendNamesEveryFailure(t *testing.T) {
	f := factories{
		core.BackendTypeDocker:     factoryOf(nil, errors.New("no socket")),
		core.BackendTypeContainerd: factoryOf(&imageBackend{healthErr: errors.New("down")}, nil),
	}

	_, err := New(choosingFrom(f))
	assert.ErrorContains(t, err, "no available backend: docker health check failed, containerd health check failed")

	err = (&Client{factories: f}).Clean(context.Background(), time.Minute, "")
	assert.ErrorContains(t, err, "failed to initialize backend")
}

type otherConfig struct{}

func (otherConfig) BackendType() core.BackendType { return "nonexistent" }

func TestNewBackendUnknownType(t *testing.T) {
	_, err := NewBackend(otherConfig{})
	assert.ErrorIs(t, err, ErrBackendNotAvailable)
}
