//go:build dreaming

package dream_test

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"

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

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{Services: []string{
		instance.Register, instance.Worker, instance.Discovery, instance.Gateway, instance.Database, instance.Primitives, storageService.Descriptor.Name,
	}})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	pids := u.Pids()[:1]

	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok)
	build := func(fixture string) string {
		code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", fixture))
		require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures")
		cid, err := store.Put(t.Context(), bytes.NewReader(code))
		require.NoError(t, err)
		return cid
	}
	first, second := build("handler.wasm"), build("cacher.wasm")

	r := publish(t, pids, deployed{project: "shop", name: "with/function/wasm", host: "hello.example.com"}, artifacts{"": first})[0]

	before := r.ask(t, http.MethodGet, "/")
	require.Equal(t, 200, before.Code, "the first build answered")
	require.Equal(t, "/", before.Header().Get("x-path"), "and it is the handler")

	stagedOn(t, pids, "shop", map[string]string{r.idOf(t, "package", "hello_code"): second})

	after := r.ask(t, http.MethodGet, "/")
	require.Empty(t, after.Header().Get("x-path"), "the instance warmed by the first build answered after the push")
	require.Equal(t, 400, after.Code, "the second build answers a path that names no operation")
	require.Equal(t, "unknown op", after.Body.String())
}
