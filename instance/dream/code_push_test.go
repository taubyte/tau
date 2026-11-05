//go:build dreaming

package dream_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	storageService "github.com/taubyte/tau/services/storage"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
)

// A CODE PUSH CHANGES WHAT RUNS AND NOTHING ELSE. The build publishes a new artifact
// for the same resource; the configuration, and so the compiled object, is untouched.
// The function served before the push has a warm instance, and the next request must
// still be answered by the code just built.
func TestACodePushIsWhatTheNextRequestRuns(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{
			instance.Register, instance.Worker, instance.Discovery, instance.Gateway, instance.Database, instance.Primitives,
			storageService.Descriptor.Name, instance.Builder,
		},
		// A node may run the previous artifact for this long after a build, so the
		// push is seen within it.
		Configure: func(_ int, c *instance.Config) { c.Worker.PointerTTL = 100 * time.Millisecond },
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	pids := u.Pids()[:1]

	fixture := func(name string) []byte {
		code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", name))
		require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures")
		return code
	}

	at := deployed{name: "with/function/wasm", host: "hello.example.com"}
	r := publish(t, u, pids, at, artifacts{"": fixture("handler.wasm")})[0]

	before := r.ask(t, http.MethodGet, "/")
	require.Equal(t, 200, before.Code, "the first build answered")
	require.Equal(t, "/", before.Header().Get("x-path"), "and it is the handler")

	_, _, built := project(t, at.name, artifacts{"": fixture("cacher.wasm")})
	build(t, u, r.project, at.name, built)

	var after *httptest.ResponseRecorder
	require.Eventually(t, func() bool {
		after = r.ask(t, http.MethodGet, "/")
		return after.Code == 400
	}, time.Minute, 50*time.Millisecond, "the second build never answered")
	require.Empty(t, after.Header().Get("x-path"), "the instance warmed by the first build is not what answers after the push")
	require.Equal(t, "unknown op", after.Body.String(), "the second build answers a path that names no operation")
}
