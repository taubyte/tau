//go:build dreaming

package dream_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/specs"
)

// A REAL COMPONENT, REACHING REAL STATE, ACROSS REAL NODES.
//
// Every link in this chain is proven somewhere else with its neighbours faked:
// the ABI against a fake store (pkg/invoke/wasm), the grant against a fake tier
// (services/worker), the backing against a fake grant (node). What none of them
// can show is that the four agree — that the label an author wrote reaches the
// rows a kvdb replicated, through a component nobody in this repository wrote
// the bindings for.
//
// So this is the only test in the tree where a wasm guest calls a host function
// that writes to a replicated store. Nothing is faked: the universe's nodes
// build their own wasm tier, their own cache backing and their own kvdb.
//
// AND THE PROJECT IS AUTHORED RATHER THAN TRANSCRIBED, for project_test's
// reason. The object this file used to publish was written by hand, so it was a
// statement of what the compiler was believed to produce and nothing ever
// checked it — and it had already drifted, in the two places the queue's
// conversion found first. It omitted `execution/timeout`, which is Required(),
// so tcc would have REFUSED the very project this test claimed to be running;
// and it omitted `implements`, which the vocabulary defaults to `handler`, so
// the field the authorizer path turns on (services/worker/resolve.go:502) was
// absent from every invocation here. projects/with/cache is compiled instead,
// which is the only way a cache's declaration and a cache's behaviour are
// tested as one thing.

// cacherWASM is the same fixture pkg/invoke/wasm runs, addressed here by the
// cid the asset store gives it.
func cacherWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "cacher.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/cacher")
	return code
}

// THESE OUTLIVED THIS FILE'S OWN OBJECT, and they are not cache's. Every other
// hand-built project in this package — instance_test, edge_test, cron_test,
// storage_test, bus_test, channel_test — is keyed by them, so they are spelled
// once here rather than six times. They are grandfathered ids and NOT the
// corpus rule's: base58btc(0x12 0x20 || sha256(name)) over `cache` is
// QmUg4HC6eXGm73Pjb6NSrQVKeywXznE31MFwPBVWdKDQAU, which is what
// projects/with/cache/project.yaml carries. Nothing compares the two, because a
// compiled object's id is an index prefix locate() cuts and never an isolation
// boundary — see deployed.
const (
	cacheProjectID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	cacheFuncID    = "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"
)

// target is the workload this package invokes.
//
// `cacher` IS A FILENAME NOW, for the two tests below: it is the stem of
// projects/with/cache/function/cacher.yaml, and a rename there stops this
// resolving. The tests that still assemble their own object spell the same
// string as a `"name"` instead.
func target() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "cacher"}
}

// TestAGuestWritesAndReadsAReplicatedCache — the whole chain, in one universe.
//
// ONE NODE, because what is under test here is the reach and not the crossing:
// the test below does the crossing.
//
// THE HOST IS INERT. deploy names one because a register keeps its pointers by
// fqdn, but every request is put to the worker directly — the project declares
// no zone and no gateway, exactly as the object it replaces did.
func TestARealComponentReachesARealCache(t *testing.T) {
	// NO INJECTED TIER. The nodes build their own, which is the point: an
	// injected one would be the test's wasm runtime with the test's cache
	// backing, and what is being proven is the node's.
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "with/cache/crdt", "sessions.example.com", cacherWASM(t))

	// The grant survived the compile, the publish and the resolve.
	//
	// NOTHING RETRIES HERE, and that is a property of this project rather than
	// an omission. The queue's first send waits out a raft bootstrap; a crdt
	// cache opens no group at all, and deploy published on the very node being
	// asked — so a resolve that needs a second attempt is a fault and not a
	// cold cloud.
	spec, err := r.node.Worker().Resolve(t.Context(), target())
	require.NoError(t, err)
	require.Equal(t, "wasm", spec.Runtime)
	require.Len(t, spec.Reaches, 1)
	require.Equal(t, "sessions", spec.Reaches[0].Label)
	// THE HANDLE IS ASKED OF THE COMPILED PROJECT rather than restated. A
	// hardcoded id here would be a second statement of what the compiler
	// produced, which is the one thing these projects exist to stop — see idOf.
	require.Equal(t, "shop/prod/"+r.idOf(t, "cache", "sessions"), spec.Reaches[0].Handle,
		"the resolved id, not the label")
	require.Empty(t, spec.Reaches[0].Refused)

	// A ROUND TRIP THROUGH THE GUEST: it writes the body into the cache and
	// reads it back, so what comes out came through the host function.
	res, err := r.onCarrying(t.Context(), target(), http.MethodPost, "/roundtrip/sessions/user:1", "alice")
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "alice", string(res.Body))

	// AND THE ROW IS REALLY THERE, read back through a second invocation so
	// the value survived the instance rather than living in it.
	res, err = r.onCarrying(t.Context(), target(), http.MethodPost, "/get/sessions/user:1", "")
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "alice", string(res.Body))

	// A key nobody wrote is a miss and not an outage.
	res, err = r.onCarrying(t.Context(), target(), http.MethodPost, "/get/sessions/user:2", "")
	require.NoError(t, err)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "not-found", string(res.Body))

	// And a cache the config never granted is refused BY NAME, with the
	// instance still alive afterwards.
	res, err = r.onCarrying(t.Context(), target(), http.MethodPost, "/get/somebody-elses/k", "")
	require.NoError(t, err)
	require.Equal(t, "no-such-cache:somebody-elses", string(res.Body))
}

// TestAValueWrittenOnOneNodeIsReadableOnAnother. A cache is a replicated store
// and nothing has ever proven a workload's own data crosses — every other test
// of replication is about the register.
//
// THE COMPONENT GOES INTO ONE STORE AND IS READ FROM BOTH. This test used to
// Put it into every node's store and then Put it a second time to learn the
// cid, which read as though the second Put mattered; it never did, because the
// same bytes always yield the same id. deployFleet stores it once, and the
// second node fetching it by cid over the swarm is pkg/assets' own claim —
// exercised rather than worked around.
func TestACacheReplicatesBetweenNodes(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 2, "with/cache/crdt", "sessions.example.com", cacherWASM(t))
	first, second := cloud[0], cloud[1]

	// Written on the first node, through its guest.
	res, err := first.onCarrying(t.Context(), target(), http.MethodPost, "/put/sessions/shared", "crosses")
	require.NoError(t, err)
	require.Equal(t, 204, res.Status, string(res.Body))

	// Read on the second, through ITS guest — which means the compiled project
	// reached it, its own tier started the component, and its own kvdb had the
	// row. Eventually, because a CRDT is eventually consistent and that is
	// exactly what this cache declared.
	require.Eventually(t, func() bool {
		res, err := second.onCarrying(t.Context(), target(), http.MethodPost, "/get/sessions/shared", "")
		return err == nil && res.Status == 200 && string(res.Body) == "crosses"
	}, 60*time.Second, 500*time.Millisecond, "a workload's own data never crossed the swarm")
}
