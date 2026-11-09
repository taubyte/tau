//go:build dreaming

package dream_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A real component reaching real replicated state across real nodes: the label an
// author wrote reaches the rows a kvdb replicated, through generated bindings.
// projects/with/cache is compiled.

// cacherWASM is the same fixture pkg/invoke/wasm runs, addressed here by the
// cid the asset store gives it.
func cacherWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "cacher.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/cacher")
	return code
}

// Shared ids for the hand-built projects in this package, not derived per the
// corpus rule; nothing compares them, since a compiled object's id is an index
// prefix and never an isolation boundary (see deployed).
const (
	cacheProjectID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	cacheFuncID    = "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"
)

func TestARealComponentReachesARealCache(t *testing.T) {
	r := deploy(t, "with/cache/crdt", "sessions.example.com", cacherWASM(t))

	// The grant survived compile, publish and resolve. No retry: a crdt cache opens no
	// group and deploy published on this node.
	spec, err := r.node.Worker().Resolve(t.Context(), r.function("cacher"))
	require.NoError(t, err)
	require.Equal(t, "wasm", spec.Runtime)
	require.Len(t, spec.Reaches, 1)
	require.Equal(t, "sessions", spec.Reaches[0].Label)
	// The handle is asked of the compiled project rather than restated (idOf).
	require.Equal(t, r.project+"/prod/"+r.idOf(t, "cache", "sessions"), spec.Reaches[0].Handle,
		"the resolved id, not the label")
	require.Empty(t, spec.Reaches[0].Refused)

	// A ROUND TRIP THROUGH THE GUEST: it writes the body into the cache and
	// reads it back, so what comes out came through the host function.
	res, err := r.onCarrying(t.Context(), r.function("cacher"), http.MethodPost, "/roundtrip/sessions/user:1", "alice")
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "alice", string(res.Body))

	// AND THE ROW IS REALLY THERE, read back through a second invocation so
	// the value survived the instance rather than living in it.
	res, err = r.onCarrying(t.Context(), r.function("cacher"), http.MethodPost, "/get/sessions/user:1", "")
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "alice", string(res.Body))

	// A key nobody wrote is a miss and not an outage.
	res, err = r.onCarrying(t.Context(), r.function("cacher"), http.MethodPost, "/get/sessions/user:2", "")
	require.NoError(t, err)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "not-found", string(res.Body))

	// And a cache the config never granted is refused BY NAME, with the
	// instance still alive afterwards.
	res, err = r.onCarrying(t.Context(), r.function("cacher"), http.MethodPost, "/get/somebody-elses/k", "")
	require.NoError(t, err)
	require.Equal(t, "no-such-cache:somebody-elses", string(res.Body))
}

func TestACacheReplicatesBetweenNodes(t *testing.T) {
	cloud := deployFleet(t, 2, "with/cache/crdt", "sessions.example.com", cacherWASM(t))
	first, second := cloud[0], cloud[1]

	// Written on the first node, through its guest.
	res, err := first.onCarrying(t.Context(), first.function("cacher"), http.MethodPost, "/put/sessions/shared", "crosses")
	require.NoError(t, err)
	require.Equal(t, 204, res.Status, string(res.Body))

	// Read on the second node through its own guest, tier and kvdb; eventually, as the
	// cache declared crdt.
	require.Eventually(t, func() bool {
		res, err := second.onCarrying(t.Context(), second.function("cacher"), http.MethodPost, "/get/sessions/shared", "")
		return err == nil && res.Status == 200 && string(res.Body) == "crosses"
	}, 60*time.Second, 500*time.Millisecond, "a workload's own data never crossed the swarm")
}
