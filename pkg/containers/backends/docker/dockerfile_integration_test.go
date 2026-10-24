//go:build docker_integration

package docker

import (
	"archive/tar"
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/containers/reserve"
)

// fetchServer answers whatever the daemon fetches of it, and counts it by path.
type fetchServer struct {
	host string

	mu   sync.Mutex
	hits map[string]int
}

func serveFetches(t *testing.T) *fetchServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	fetches := &fetchServer{host: listener.Addr().String(), hits: map[string]int{}}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.mu.Lock()
		fetches.hits[r.URL.Path]++
		fetches.mu.Unlock()
		io.WriteString(w, "fetched")
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return fetches
}

func (f *fetchServer) count(at string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[at]
}

// buildClassic builds a context with the classic builder, the one a restricted
// build is pinned to, on the daemon's own network.
func buildClassic(t *testing.T, backend *DockerBackend, tag string, buildContext io.Reader, dockerfile string) error {
	t.Helper()
	t.Cleanup(func() {
		backend.client.ImageRemove(context.Background(), tag, client.ImageRemoveOptions{Force: true, PruneChildren: true})
	})
	return backend.build(t.Context(), buildContext, client.ImageBuildOptions{
		Tags:       []string{tag},
		Remove:     true,
		Dockerfile: dockerfile,
		Version:    build.BuilderV1,
	})
}

func labelOf(t *testing.T, backend *DockerBackend, tag, label string) string {
	t.Helper()
	built, err := backend.client.ImageInspect(t.Context(), tag)
	require.NoError(t, err)
	require.NotNil(t, built.Config)
	return built.Config.Labels[label]
}

// TestReadDockerfileAgreesWithTheDaemon_Integration builds every context and
// every Dockerfile the unit tests check with a real daemon.
//
// For a context: where the spool reads a Dockerfile, the daemon built that one,
// and built the one put where it was read from once the context is spooled,
// which is where a restricted build puts its pinned Dockerfile; where the spool
// reads none, the daemon built nothing. A context it refuses has nothing to
// agree on.
//
// For a Dockerfile: one the check lets through makes the daemon fetch nothing,
// as written and as a restricted build sends it, and one it refuses makes the
// daemon fetch, unless the case says the check refuses more there than the
// daemon fetches.
func TestReadDockerfileAgreesWithTheDaemon_Integration(t *testing.T) {
	backend, err := New(core.DockerConfig{})
	require.NoError(t, err)
	defer backend.client.Close()

	for i, c := range daemonContexts(t) {
		if c.refused != "" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			tag := fmt.Sprintf("tau-dockerfile-agrees:%d", i)
			err := backend.Image(tag).Build(t.Context(), &core.DockerfileBuild{
				Context:    bytes.NewReader(c.context),
				Dockerfile: c.dockerfile,
			})
			t.Cleanup(func() {
				backend.client.ImageRemove(context.Background(), tag, client.ImageRemoveOptions{Force: true, PruneChildren: true})
			})
			if c.pick == "" {
				require.Error(t, err, "the daemon built from a context with no Dockerfile read in it")
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.pick, labelOf(t, backend, tag, "pick"), "the daemon built another Dockerfile than the one read")

			name := cmp.Or(c.dockerfile, "Dockerfile")
			spooled, err := spoolContext(bytes.NewReader(c.context), t.TempDir(), reserve.Default)
			require.NoError(t, err)
			defer spooled.remove()
			header, _, at, err := spooled.dockerfile(name)
			require.NoError(t, err)
			pinned := regular("", "pinned").body
			require.NoError(t, spooled.put(&tar.Header{Typeflag: tar.TypeReg, Name: at, Mode: header.Mode, Size: int64(len(pinned))}, strings.NewReader(pinned), nil))
			sent, err := spooled.finish()
			require.NoError(t, err)
			require.NoError(t, buildClassic(t, backend, tag+"-spooled", sent, name))
			require.Equal(t, "pinned", labelOf(t, backend, tag+"-spooled", "pick"), "the daemon built another Dockerfile than the one put where it was read")
		})
	}

	fetches := serveFetches(t)
	for i := range fetchCases("", "") {
		at := fmt.Sprintf("/%d", i)
		c := fetchCases(fetches.host, at)[i]
		t.Run("fetch/"+c.name, func(t *testing.T) {
			_, err := checkDockerfile([]byte(c.dockerfile))
			require.Equal(t, c.refused, err != nil, "%v", err)

			context := fetchContext(t, c.dockerfile)
			built := buildClassic(t, backend, fmt.Sprintf("tau-fetch-agrees:%d", i), bytes.NewReader(context), "Dockerfile")
			fetched := fetches.count(at) > 0
			switch {
			case !c.refused:
				require.False(t, fetched, "the check lets through a Dockerfile the daemon fetches for")
			case c.quiet:
				require.False(t, fetched, "the daemon fetched for a Dockerfile said to be refused beyond what it fetches")
			default:
				require.True(t, fetched, "the daemon did not fetch for a Dockerfile the check refuses as fetching (build: %v)", built)
			}
			if c.refused {
				return
			}

			spooled, err := backend.restrictContext(t.Context(), bytes.NewReader(context), "Dockerfile", t.TempDir(), reserve.Default)
			require.NoError(t, err)
			defer spooled.remove()
			sent, err := spooled.finish()
			require.NoError(t, err)
			require.NoError(t, buildClassic(t, backend, fmt.Sprintf("tau-fetch-sent:%d", i), sent, "Dockerfile"))
			require.Zero(t, fetches.count(at))
		})
	}
}

// TestABaseImageIsCheckedAndBuiltByID_Integration: a base image's ONBUILD ADD
// fetches when a stage is built FROM it, so a restricted build FROM it is
// refused; and a tag moved to such an image after the check cannot swap it in,
// because what is built names the checked image by id.
func TestABaseImageIsCheckedAndBuiltByID_Integration(t *testing.T) {
	backend := newTestBackend(t)
	fetches := serveFetches(t)
	dockerfile := func(body string) io.Reader {
		return bytes.NewReader(contextOf(t, member{name: "Dockerfile", kind: tar.TypeReg, body: body}))
	}
	const (
		trap   = "tau-base-trap:1"
		clean  = "tau-base-clean:1"
		moving = "tau-base-moving:1"
	)
	t.Cleanup(func() {
		backend.client.ImageRemove(context.Background(), moving, client.ImageRemoveOptions{Force: true})
	})
	retag := func(source string) {
		_, err := backend.client.ImageTag(t.Context(), client.ImageTagOptions{Source: source, Target: moving})
		require.NoError(t, err)
	}

	require.NoError(t, buildClassic(t, backend, trap, dockerfile("FROM scratch\nONBUILD ADD http://"+fetches.host+"/trap /x\n"), "Dockerfile"))
	require.NoError(t, buildClassic(t, backend, clean, dockerfile("FROM scratch\nLABEL base=clean\n"), "Dockerfile"))

	_, err := backend.restrictContext(t.Context(), dockerfile("FROM "+trap+"\n"), "Dockerfile", t.TempDir(), reserve.Default)
	require.ErrorContains(t, err, "base image "+trap)
	require.NoError(t, buildClassic(t, backend, "tau-base-child:1", dockerfile("FROM "+trap+"\n"), "Dockerfile"))
	require.Equal(t, 1, fetches.count("/trap"), "a stage built FROM the trap fetches")

	retag(clean)
	spooled, err := backend.restrictContext(t.Context(), dockerfile("FROM "+moving+"\nLABEL child=yes\n"), "Dockerfile", t.TempDir(), reserve.Default)
	require.NoError(t, err)
	defer spooled.remove()
	retag(trap)
	sent, err := spooled.finish()
	require.NoError(t, err)
	require.NoError(t, buildClassic(t, backend, "tau-base-pinned:1", sent, "Dockerfile"))
	require.Equal(t, 1, fetches.count("/trap"), "the build ran a trigger of the image the tag moved to")
	require.Equal(t, "clean", labelOf(t, backend, "tau-base-pinned:1", "base"))
	require.Equal(t, "yes", labelOf(t, backend, "tau-base-pinned:1", "child"))

	require.NoError(t, buildClassic(t, backend, "tau-base-moved:1", dockerfile("FROM "+moving+"\n"), "Dockerfile"))
	require.Equal(t, 2, fetches.count("/trap"), "built by its tag, the base the tag moved to fetches")
}
