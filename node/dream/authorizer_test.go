//go:build dreaming

package dream_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

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
//
// AND THE PROJECT IS AUTHORED RATHER THAN TRANSCRIBED, for project_test's
// reason. The object this file used to publish was written by hand, so it was a
// statement of what the compiler was believed to produce and nothing ever
// checked it — and it had drifted in both of the ways every conversion before it
// found. Neither function carried `execution/timeout`, which is Required(), so
// tcc would have REFUSED the very project this test claimed to be running; and
// all five resource ids were minted rather than derived — the two functions a
// two-character edit of fixture_test's own id, the three caches an incrementing
// base58 tail over one digest. projects/with/authorizer is compiled instead.
//
// THE ONE FIELD THIS TEST IS ABOUT IS THE ONE THE LITERAL GOT RIGHT, and it had
// no choice: `implements: authorizer` is the only non-default world in the
// corpus, and a guard that says anything else is refused at resolve rather than
// instantiated and handed a question it would read as a request
// (services/worker/resolve.go:507). What the literal left to the default was the
// CALLER's — the reaching function carried no `implements` at all, so the
// setting reached the tier absent and ran as a handler only because an empty
// world IS the handler (pkg/invoke/wasm/wasm.go:386). Compiled, it says
// `handler` out loud, which is what the assertions below now read.

func guardWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "guard.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/guard")
	return code
}

// guardedTarget is the guest, and keeperTarget the function that decides for it.
//
// BOTH ARE FILENAMES UNDER projects/with/authorizer/function, and a rename there
// stops these resolving.
func guardedTarget() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "reacher"}
}

func keeperTarget() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "keeper"}
}

// TestAGuestReachesAGuardedCacheAndASecondComponentDecides.
//
// THE THREE CACHES ARE THE GUARD FIXTURE'S THREE ARMS, selected by the cache's
// NAME — which is what the caller writes in its access block and therefore what
// travels to the guard as the target's label. Renaming one in the corpus moves
// it to a different arm.
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

	// TWO ARTIFACTS, WHICH IS WHY THIS PROJECT CANNOT GO THROUGH deploy. Every
	// other corpus project runs one binary under several names; here the guard
	// is a different component built against a different world, and pointing it
	// at the cacher would be the very substitution `implements` exists to refuse.
	//
	// AND THE UNIVERSE IS THE SMALLEST ONE THAT ANSWERS: no gateway, no zone and
	// no host, because both halves of this are put to the worker directly.
	compiled, _, pointed := project(t, "with/cache/authorizer", artifacts{"": reacher, "keeper": keeper})
	stagedOn(t, pids, "shop", pointed)

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: compiled,
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

	// AND THE TWO WORLDS SURVIVED THE COMPILE, which is what the hand-built
	// object could only assert about itself. The guard's is the corpus's one
	// non-default `implements`, and it is the string the tier instantiates it
	// under; the guest's is the default the vocabulary fills in, and the
	// difference between the two is what keeps a handler from standing in for an
	// authorizer.
	require.Equal(t, "handler", spec.Settings["implements"])

	guard, err := n.Worker().Resolve(t.Context(), keeperTarget())
	require.NoError(t, err)
	require.Equal(t, "authorizer", guard.Settings["implements"])
	require.Equal(t, "authorize", guard.Settings["execution/call"])

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
