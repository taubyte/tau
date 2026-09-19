//go:build dreaming

package dream_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// THE ONLY PLACE BOTH HALVES ARE REAL. Everything else about an authorizer is
// proven with its neighbour faked: the ABI against a fake seam
// (pkg/invoke/wasm), the seam against a node with no tier (node), the grant
// against a hand-built project (services/worker). What none of them can show is
// that a guest reaching a guarded cache causes a SECOND component, built against
// a DIFFERENT world, to be resolved, instantiated and asked — and that what it
// answers is what the first one is allowed to do.
//
// So this runs two real components: fixtures/cacher reaches the cache, and
// fixtures/guard decides.

func guardWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "guard.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/guard")
	return code
}

const (
	guardedFuncID  = "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYGa"
	keeperFuncID   = "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYGb"
	openCacheID    = "QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioG1"
	closedCacheID  = "QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioG2"
	unsureCacheID  = "QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioG3"
	guardedProject = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
)

func guardedTarget() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "reacher"}
}

// withGuardedCaches is one guest, one authorizer and three caches it guards.
//
// The three are the guard fixture's three arms, selected by the cache's NAME,
// which is what the caller writes in its access block and therefore what travels
// to the guard as the target's label.
func withGuardedCaches(reacherCID, keeperCID string) []byte {
	cache := func(name string) map[string]any {
		return map[string]any{
			"name": name, "consensus": "crdt", "size": 1000000, "ttl": float64(time.Hour),
			"authorizer": map[string]any{"name": "keeper"},
		}
	}

	doc := map[string]any{
		"object": map[string]any{
			"id":   guardedProject,
			"name": "shop",
			"function": map[string]any{
				guardedFuncID: map[string]any{
					"name":      "reacher",
					"execution": map[string]any{"call": "handle", "memory": 33554432},
					"source":    map[string]any{"kind": "component", "component": reacherCID},
					"cache": map[string]any{
						"sessions":    map[string]any{"ops": []any{"get", "put", "delete"}},
						"forbidden":   map[string]any{"ops": []any{"get", "put"}},
						"undecidable": map[string]any{"ops": []any{"get"}},
					},
				},
				keeperFuncID: map[string]any{
					"name":       "keeper",
					"implements": "authorizer",
					"execution":  map[string]any{"call": "authorize", "memory": 8388608},
					"source":     map[string]any{"kind": "component", "component": keeperCID},
				},
			},
			"cache": map[string]any{
				openCacheID:   cache("sessions"),
				closedCacheID: cache("forbidden"),
				unsureCacheID: cache("undecidable"),
			},
		},
		"indexes": map[string]any{
			"projects/" + guardedProject + "/function/reacher/links": []any{
				"branches/main/projects/" + guardedProject + "/function/" + guardedFuncID},
			"projects/" + guardedProject + "/function/keeper/links": []any{
				"branches/main/projects/" + guardedProject + "/function/" + keeperFuncID},
			"projects/" + guardedProject + "/cache/sessions/links": []any{
				"branches/main/projects/" + guardedProject + "/cache/" + openCacheID},
			"projects/" + guardedProject + "/cache/forbidden/links": []any{
				"branches/main/projects/" + guardedProject + "/cache/" + closedCacheID},
			"projects/" + guardedProject + "/cache/undecidable/links": []any{
				"branches/main/projects/" + guardedProject + "/cache/" + unsureCacheID},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// TestAGuestReachesAGuardedCacheAndASecondComponentDecides.
func TestAGuardDecidesForARealGuest(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	m, err := dream.New(t.Context())
	require.NoError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)
	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			nodeDream.Protocol: {Others: nodeDream.Services(node.Register, node.Worker, node.Stats)},
		},
	}))

	pids, err := u.GetServicePids(nodeDream.Protocol)
	require.NoError(t, err)
	require.NotEmpty(t, pids)

	n := nodeOf(t, pids[0])
	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok)

	reacher, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)
	keeper, err := store.Put(t.Context(), bytes.NewReader(guardWASM(t)))
	require.NoError(t, err)

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: withGuardedCaches(reacher, keeper),
	})
	require.NoError(t, err)

	// THE GRANT CARRIES THE GUARD, resolved where the project was open.
	spec, err := n.Worker().Resolve(t.Context(), guardedTarget())
	require.NoError(t, err)
	require.Len(t, spec.Reaches, 3)
	for _, g := range spec.Reaches {
		require.Empty(t, g.Refused, g.Label)
		require.Equal(t, "keeper", g.Settings["authorizer/name"], g.Label)
	}

	ask := func(path string, body []byte) *invoke.Response {
		t.Helper()
		res, err := n.Worker().Invoke(t.Context(), guardedTarget(), &invoke.Request{
			Method: "POST", Path: path, Body: body,
		})
		require.NoError(t, err)
		return res
	}

	// GRANTED, and the whole chain ran to get there: the guest called the host,
	// the host resolved a second function, instantiated it as an authorizer and
	// asked it, and the answer let the write through to a real store.
	require.Equal(t, 204, ask("/put/sessions/a", []byte("alice")).Status)

	res := ask("/get/sessions/a", nil)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "alice", string(res.Body))

	// A DECIDED REFUSAL. The guard grants nothing on this one, so the guest is
	// denied by name and stays alive to be asked again.
	res = ask("/put/forbidden/a", []byte("x"))
	require.Contains(t, string(res.Body), "denied", string(res.Body))

	// AND A REFUSAL TO DECIDE, which is a different sentence. Serving it anyway
	// would grant exactly the access the target said somebody else decides.
	res = ask("/get/undecidable/a", nil)
	require.Contains(t, string(res.Body), "denied", string(res.Body))
	require.Contains(t, string(res.Body), "opinion", string(res.Body))

	// AN OP OUTSIDE THE KEPT SET IS DENIED FROM THE SET, not by asking again.
	// The guard grants get and put and never delete, and the answer it gave to
	// the very first question — a `put` — is what refuses this one. That is the
	// whole reason the contract says the answer is the complete set: a host
	// entitled to keep it keeps ALL of it, including what it does not grant.
	res = ask("/delete/sessions/a", nil)
	require.Contains(t, string(res.Body), "denied", string(res.Body))
	require.Contains(t, string(res.Body), "delete", string(res.Body))

	// AND THE GUEST IS STILL ALIVE after three denials, which is the whole
	// reason a refusal is an answer rather than a trap.
	require.Equal(t, 200, ask("/get/sessions/a", nil).Status)
	require.Equal(t, "alice", string(ask("/get/sessions/a", nil).Body))
}

// admitterWASM is the instance validator fixture — a real component exporting
// `admit`, which is what the `instances:` block's validator has to be now that
// its world is built.
func admitterWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "admitter.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/admitter")
	return code
}
