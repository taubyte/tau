package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

// The three policy calls this file makes, each pinned by a test, because none
// of them is forced by the ABI and all three are the kind of thing that gets
// quietly reversed:
//
//   - an answer is KEPT per caller and target, which is what keeps a wasm
//     instantiation off a cache's hot path
//   - the key carries the caller's REVISION, so a redeploy re-asks
//   - an UNDECIDED answer is not kept, because a guard that declined may decide

func guardedGrant() invoke.Grant {
	return invoke.Grant{
		Kind: "cache", Label: "orders", Handle: "shop/prod/QmOrders", Ops: []string{"get"},
		Settings: map[string]string{
			settingAuthorizer:      "keeper",
			settingAuthorizerScope: "/shop",
		},
	}
}

func caller(revision string) invoke.Spec {
	return invoke.Spec{
		Project: "shop", Environment: "prod", Kind: "function", Label: "checkout",
		Scope: []string{"shop"}, Revision: revision,
	}
}

// keyFor is what Ops builds, reproduced here so a test can seed the cache the
// way a previous answer would have.
func keyFor(spec invoke.Spec, grant invoke.Grant) string {
	return strings.Join([]string{spec.Revision, spec.Kind, spec.Label, strings.Join(spec.Scope, "/"), grant.Handle}, "\x00")
}

// TestAKeptAnswerIsReturnedWithoutRunningAnything. The node here has no
// component tier at all, so an answer that comes back proves it came from the
// cache — asking would have failed.
func TestAKeptAnswerNeedsNoTier(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker}})
	require.Nil(t, n.wasmTier(), "this node runs no components")

	a := &authorizers{node: n}
	grant := guardedGrant()
	a.remember(keyFor(caller("r1"), grant), []string{"get", "put"})

	ops, err := a.Ops(t.Context(), caller("r1"), grant, "get")
	require.NoError(t, err)
	require.Equal(t, []string{"get", "put"}, ops)
}

// TestAREDEPLOYREASKS. A revision is the immutable build identity, so a
// workload whose code changed is a different caller as far as a guard is
// concerned — which is the case where a stale answer would be most wrong.
func TestANewRevisionIsANewQuestion(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker}})

	a := &authorizers{node: n}
	grant := guardedGrant()
	a.remember(keyFor(caller("r1"), grant), []string{"get", "put"})

	_, err := a.Ops(t.Context(), caller("r2"), grant, "get")
	require.Error(t, err, "the old answer must not be reused for new code")
	require.Contains(t, err.Error(), "cannot run one", "it fell through to asking")
}

// TestTheTARGETISPARTOFTHEKEYTOO, so one guarded resource's answer is never
// handed out for another.
func TestADifferentTargetIsADifferentQuestion(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker}})

	a := &authorizers{node: n}
	first := guardedGrant()
	a.remember(keyFor(caller("r1"), first), []string{"get", "put", "delete"})

	other := guardedGrant()
	other.Handle = "shop/prod/QmSomethingElse"

	_, err := a.Ops(t.Context(), caller("r1"), other, "get")
	require.Error(t, err)
}

// TestANodeThatCannotAskSaysSo rather than granting. addressed turns this into
// a denial, which is the fail-closed half of the policy.
func TestANodeWithNoTierCannotAnswer(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker}})

	_, err := (&authorizers{node: n}).Ops(t.Context(), caller("r1"), guardedGrant(), "get")
	require.Error(t, err)
	require.Contains(t, err.Error(), "keeper", "and names the guard it could not run")
}

// TestAHandleThatNamesNoDeploymentIsNotAskedAbout.
func TestAGuardedHandleMustNameADeployment(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker}})
	a := &authorizers{node: n}
	a.node.wasm = nil

	grant := guardedGrant()
	grant.Handle = "nonsense"

	_, err := a.Ops(t.Context(), caller("r1"), grant, "get")
	require.Error(t, err)
}

// TestAnInstanceTravelsToTheGuard, so a guard on a store that holds many can
// decide per instance rather than per store.
func TestTheInstanceIsReadOffTheHandle(t *testing.T) {
	require.Equal(t, "room-42", instanceOf("shop/prod/QmChat/room-42"))
	require.Empty(t, instanceOf("shop/prod/QmChat"), "a store that holds one has none")
	require.Empty(t, instanceOf("nonsense"))
}
