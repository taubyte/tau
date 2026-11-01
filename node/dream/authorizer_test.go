//go:build dreaming

package dream_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// Both halves of an authorizer real: fixtures/cacher reaches a guarded cache and
// fixtures/guard, a second component in another world, is resolved, instantiated
// and asked, its answer deciding the first's access. projects/with/authorizer is
// compiled, so the guard's `implements: authorizer` and the caller's default
// `handler` come from the compiler.

func guardWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "guard.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/guard")
	return code
}

// guardedTarget is the guest and keeperTarget the function deciding for it,
// filenames under projects/with/authorizer/function.
func guardedTarget() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "reacher"}
}

func keeperTarget() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "keeper"}
}

func TestAGuardDecidesForARealGuest(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{node.Register, node.Worker, node.Discovery},
	})
	require.NoError(t, err)
	defer u.Close()

	pids := u.Pids()
	require.NotEmpty(t, pids)

	n := nodeOf(t, pids[0])
	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok)

	reacher, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)
	keeper, err := store.Put(t.Context(), bytes.NewReader(guardWASM(t)))
	require.NoError(t, err)

	// Two artifacts, so this cannot go through deploy: the guard is a different
	// component in a different world. No gateway or zone; both halves are put to the
	// worker directly.
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

	// The compiled worlds: the guard's `authorizer`, the guest's defaulted `handler`.
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

	// An op outside the kept set is denied from the set, not by asking again: the
	// first answer was complete.
	res = ask("/delete/sessions/a", nil)
	require.Contains(t, string(res.Body), "denied", string(res.Body))
	require.Contains(t, string(res.Body), "delete", string(res.Body))

	// AND THE GUEST IS STILL ALIVE after three denials, which is the whole
	// reason a refusal is an answer rather than a trap.
	require.Equal(t, 200, ask("/get/sessions/a", nil).Status)
	require.Equal(t, "alice", string(ask("/get/sessions/a", nil).Body))
}

// admitterWASM is the instance validator fixture, exporting `admit`.
func admitterWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "admitter.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/admitter")
	return code
}
