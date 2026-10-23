package docker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

// fakeDaemon answers the docker HTTP API from a table of routes, keyed by
// "METHOD /path" with the version prefix stripped. A route it has no entry
// for fails with a daemon error, which is how the error paths are reached.
type fakeDaemon struct {
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	seen   []string
}

var versionPrefix = regexp.MustCompile(`^/v[0-9.]+`)

func (d *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + versionPrefix.ReplaceAllString(r.URL.Path, "")
	d.mu.Lock()
	d.seen = append(d.seen, key)
	handler, ok := d.routes[key]
	d.mu.Unlock()

	if key == "GET /_ping" || key == "HEAD /_ping" {
		w.Header().Set("Api-Version", "1.47")
		io.WriteString(w, "OK")
		return
	}
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"message":"daemon says no"}`)
		return
	}
	handler(w, r)
}

func (d *fakeDaemon) called(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.seen {
		if s == key {
			return true
		}
	}
	return false
}

func jsonBody(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}
}

func newFakeDaemonBackend(t *testing.T, routes map[string]http.HandlerFunc) (*DockerBackend, *fakeDaemon) {
	t.Helper()
	daemon := &fakeDaemon{routes: routes}
	server := httptest.NewServer(daemon)
	t.Cleanup(server.Close)

	backend, err := New(core.DockerConfig{
		Host:       "tcp://" + strings.TrimPrefix(server.URL, "http://"),
		APIVersion: "1.47",
	})
	require.NoError(t, err)
	assert.Equal(t, core.BackendTypeDocker, backend.BackendType())
	return backend, daemon
}

func TestDaemonErrorsSurface(t *testing.T) {
	backend, _ := newFakeDaemonBackend(t, nil)
	ctx := context.Background()

	_, err := backend.Create(ctx, &core.ContainerConfig{Image: "alpine"})
	assert.ErrorContains(t, err, "failed to create container")

	// An id the backend never created is looked up by name, and that lookup fails.
	assert.ErrorContains(t, backend.Start(ctx, "unknown"), "failed to list containers")

	backend.putContainer("known", "abc")
	assert.ErrorContains(t, backend.Start(ctx, "known"), "failed to start container")
	assert.ErrorContains(t, backend.Stop(ctx, "known"), "failed to stop container")
	assert.ErrorContains(t, backend.Remove(ctx, "known"), "failed to remove container")
	assert.ErrorContains(t, backend.Wait(ctx, "known"), "failed to wait for container")
	assert.ErrorContains(t, backend.Clean(ctx, time.Hour, "alpine"), "failed to list images")

	_, err = backend.Logs(ctx, "known")
	assert.ErrorContains(t, err, "failed to get logs")
	_, err = backend.Inspect(ctx, "known")
	assert.ErrorContains(t, err, "failed to inspect container")

	for _, op := range []func(context.Context, core.ContainerID) error{backend.Stop, backend.Remove, backend.Wait} {
		assert.ErrorContains(t, op(ctx, "unknown"), "failed to list containers")
	}
	_, err = backend.Logs(ctx, "unknown")
	assert.ErrorContains(t, err, "failed to list containers")
	_, err = backend.Inspect(ctx, "unknown")
	assert.ErrorContains(t, err, "failed to list containers")

	image := backend.Image("alpine")
	assert.False(t, image.Exists(ctx))
	assert.ErrorContains(t, image.Pull(ctx), "failed to pull image")
	assert.ErrorContains(t, image.Build(ctx, &core.DockerfileBuild{Context: strings.NewReader("")}), "failed to build image")
	_, err = image.Digest(ctx)
	assert.ErrorContains(t, err, "failed to inspect image")
	_, err = image.Tags(ctx)
	assert.ErrorContains(t, err, "failed to inspect image")
}

func TestStdinAttachFailure(t *testing.T) {
	backend, daemon := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
		"POST /containers/create": jsonBody(`{"Id":"abc"}`),
	})
	ctx := context.Background()

	id, err := backend.Create(ctx, &core.ContainerConfig{Image: "alpine", Stdin: strings.NewReader("in")})
	require.NoError(t, err)

	assert.ErrorContains(t, backend.Start(ctx, id), "attaching stdin")
	assert.False(t, daemon.called("POST /containers/abc/start"), "a container whose stdin could not attach must not start")
}

func TestGetDockerIDFindsAContainerByName(t *testing.T) {
	backend, _ := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
		"GET /containers/json": jsonBody(`[{"Id":"abc"}]`),
	})

	id, err := backend.getDockerID(context.Background(), "tau-x")
	require.NoError(t, err)
	assert.Equal(t, "abc", id)

	cached, ok := backend.lookupContainer("tau-x")
	assert.True(t, ok)
	assert.Equal(t, "abc", cached)
}

func TestGetDockerIDNotFound(t *testing.T) {
	backend, _ := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
		"GET /containers/json": jsonBody(`[]`),
	})

	_, err := backend.getDockerID(context.Background(), "tau-x")
	assert.ErrorContains(t, err, "container tau-x not found")
}

func TestInspectMapsResources(t *testing.T) {
	backend, _ := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
		"GET /containers/abc/json": jsonBody(`{"Id":"abc","Config":{"Image":"alpine"},
			"State":{"Status":"exited","ExitCode":3,"StartedAt":"2026-01-02T03:04:05.000000006Z"},
			"HostConfig":{"Memory":1048576,"MemorySwap":2097152,"CpuQuota":5000,"CpuPeriod":10000,"CpuShares":512,"PidsLimit":64}}`),
	})
	backend.putContainer("c", "abc")

	info, err := backend.Inspect(context.Background(), "c")
	require.NoError(t, err)
	assert.Equal(t, core.ContainerID("c"), info.ID)
	assert.Equal(t, "alpine", info.Image)
	assert.Equal(t, 3, info.ExitCode)
	assert.Equal(t, time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC), info.StartedAt.UTC())
	require.NotNil(t, info.Resources)
	assert.Equal(t, core.ResourceLimits{
		Memory: 1048576, MemorySwap: 2097152, CPUQuota: 5000, CPUPeriod: 10000, CPUShares: 512, PIDs: 64,
	}, *info.Resources)
}

func TestWaitHonoursCancellation(t *testing.T) {
	backend, _ := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
		"POST /containers/abc/wait": func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		},
	})
	backend.putContainer("c", "abc")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, backend.Wait(ctx, "c"), context.DeadlineExceeded)
}

func TestCleanJoinsRemoveFailures(t *testing.T) {
	backend, daemon := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
		"GET /images/json": jsonBody(`[{"Id":"sha256:old","Created":1},{"Id":"sha256:new","Created":` +
			"9999999999" + `}]`),
	})

	err := backend.Clean(context.Background(), time.Hour, "")
	assert.ErrorContains(t, err, "removing image sha256:old")
	assert.NotContains(t, err.Error(), "sha256:new", "an image younger than the age must be kept")
	assert.True(t, daemon.called("DELETE /images/sha256:old"))
	assert.False(t, daemon.called("DELETE /images/sha256:new"))
}

func TestPullAndBuildReportStreamedErrors(t *testing.T) {
	for _, line := range []string{`{"error":"bad thing"}`, `{"errorDetail":{"message":"bad thing"}}`} {
		backend, _ := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
			"POST /images/create": jsonBody(`{"status":"pulling"}` + "\n" + line + "\n"),
			"POST /build":         jsonBody("not json\n" + `{"stream":"Step 1"}` + "\n" + line + "\n"),
		})
		image := backend.Image("alpine")

		assert.ErrorContains(t, image.Pull(context.Background()), "docker pull failed: bad thing")
		assert.ErrorContains(t, image.Build(context.Background(), &core.DockerfileBuild{Context: strings.NewReader("")}),
			"docker build failed: bad thing")
	}
}

func TestImageInspectAnswers(t *testing.T) {
	backend, _ := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
		"GET /images/json":            jsonBody(`[]`),
		"GET /images/withdigest/json": jsonBody(`{"Id":"sha256:local","RepoDigests":["withdigest@sha256:remote"],"RepoTags":["withdigest:1"]}`),
		"GET /images/localonly/json":  jsonBody(`{"Id":"sha256:local","RepoDigests":["malformed"]}`),
		"DELETE /images/withdigest":   jsonBody(`[]`),
		"GET /images/missingeverywhere/json": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"no such image"}`)
		},
	})
	ctx := context.Background()

	digest, err := backend.Image("withdigest").Digest(ctx)
	require.NoError(t, err)
	assert.Equal(t, "remote", digest)

	digest, err = backend.Image("localonly").Digest(ctx)
	require.NoError(t, err)
	assert.Equal(t, "local", digest)

	tags, err := backend.Image("withdigest").Tags(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"withdigest:1"}, tags)

	// The reference filter finds nothing; the inspect fallback decides.
	assert.True(t, backend.Image("withdigest").Exists(ctx))
	assert.False(t, backend.Image("missingeverywhere").Exists(ctx))

	assert.NoError(t, backend.Image("withdigest").Remove(ctx))
}

func TestRestrictedEgressFailsClosed(t *testing.T) {
	backend, daemon := newFakeDaemonBackend(t, nil)
	ctx := context.Background()

	_, err := backend.Create(ctx, &core.ContainerConfig{Image: "alpine", Network: &core.NetworkConfig{RestrictEgress: true}})
	assert.ErrorContains(t, err, "creating the restricted-egress network")
	assert.False(t, daemon.called("POST /containers/create"), "no container may be created without its network")

	err = backend.Image("built").Build(ctx, &core.DockerfileBuild{Context: strings.NewReader(""), RestrictEgress: true})
	assert.ErrorContains(t, err, "creating the restricted-egress network")
	assert.False(t, daemon.called("POST /build"), "no build may run without its network")
}

func TestRestrictedNetworkCreatedConcurrently(t *testing.T) {
	// The create fails because another process made the network first; the
	// second inspect finding it is success.
	inspects := 0
	backend, daemon := newFakeDaemonBackend(t, map[string]http.HandlerFunc{
		"GET /networks/" + restrictedNetwork: func(w http.ResponseWriter, r *http.Request) {
			inspects++
			if inspects == 1 {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"message":"no such network"}`)
				return
			}
			jsonBody(`{"Name":"`+restrictedNetwork+`"}`)(w, r)
		},
	})

	require.NoError(t, backend.ensureRestrictedNetwork(context.Background()))
	assert.True(t, daemon.called("POST /networks/create"))
	assert.Equal(t, 2, inspects)
}
