package worker

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

// The guard resolution, against hand-built projects.
//
// NOT THE VOCABULARY'S FIXTURE, for once, and the reason is worth stating: the
// compiler already refuses a guard that is not an authorizer as `Broken`
// (checks.go, and TestAuditOfTheExample holds the fixture to it), so the fixture
// CANNOT carry the cases below. What is tested here is the second line of
// defence — a compiled object that did not come from tcc, which is the same
// thing startedBy and servedBy guard against.

const (
	guardProject = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	cacheOneID   = "QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioE1"
	guardFuncID  = "QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioE2"
	plainFuncID  = "QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioE3"
	nestedCache  = "QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioE4"
	nestedNSID   = "QmNsp5gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioX"
)

// guardedProject is one cache guarded by `authorizer`, plus a second cache one
// namespace down guarded by the SAME name with no namespace written — which is
// what proves the walk goes outward from the target.
func guardedProject(t *testing.T, guard map[string]any) *specs.Project {
	t.Helper()

	doc := map[string]any{
		"object": map[string]any{
			"id":   guardProject,
			"name": "shop",
			"function": map[string]any{
				guardFuncID: map[string]any{
					"name": "keeper", "implements": "authorizer",
					"execution": map[string]any{"call": "authorize", "memory": 8388608},
				},
				plainFuncID: map[string]any{
					"name": "plain", "implements": "handler",
					"execution": map[string]any{"call": "handle", "memory": 8388608},
				},
			},
			"cache": map[string]any{
				cacheOneID: map[string]any{
					"name": "one", "size": 1000, "consensus": "crdt", "authorizer": guard,
				},
			},
			"namespace": map[string]any{
				nestedNSID: map[string]any{
					"name": "local",
					"cache": map[string]any{
						nestedCache: map[string]any{
							// NO NAMESPACE WRITTEN, so it must find the guard in
							// the parent.
							"name": "deep", "size": 1000, "consensus": "crdt",
							"authorizer": map[string]any{"name": "keeper"},
						},
					},
				},
			},
		},
		"indexes": map[string]any{
			"projects/" + guardProject + "/function/keeper/links": []any{
				"branches/main/projects/" + guardProject + "/function/" + guardFuncID},
			"projects/" + guardProject + "/function/plain/links": []any{
				"branches/main/projects/" + guardProject + "/function/" + plainFuncID},
			"projects/" + guardProject + "/cache/one/links": []any{
				"branches/main/projects/" + guardProject + "/cache/" + cacheOneID},
			"projects/" + guardProject + "/namespace/local/links": []any{
				"branches/main/projects/" + guardProject + "/namespace/" + nestedNSID},
			"projects/" + guardProject + "/namespace/" + nestedNSID + "/cache/deep/links": []any{
				"branches/main/projects/" + guardProject + "/namespace/" + nestedNSID + "/cache/" + nestedCache},
		},
	}

	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	proj, err := specs.Load(raw)
	require.NoError(t, err)
	return proj
}

func guardOf(t *testing.T, proj *specs.Project, scope []string, name, id string) invoke.Grant {
	t.Helper()

	target, _, err := proj.Resource(scope, "cache", name)
	require.NoError(t, err)

	grant := invoke.Grant{Kind: "cache", Label: name, Ops: []string{"get"}}
	guardedBy(proj, Target{Project: "shop", Environment: "prod"}, target, id, &grant)
	return grant
}

// TestAGuardIsResolvedToTheFunctionThatAnswersIt.
func TestAGuardResolvesToAnAuthorizer(t *testing.T) {
	proj := guardedProject(t, map[string]any{"name": "keeper"})

	grant := guardOf(t, proj, nil, "one", cacheOneID)
	require.Empty(t, grant.Refused)
	require.Equal(t, "keeper", grant.Settings["authorizer/name"])
	require.Equal(t, "/", grant.Settings["authorizer/namespace"],
		"the project root, stated rather than left empty for a reader to guess")
}

// TestTheWalkIsTheTARGETsAndNotTheCALLERs. A guard belongs to the thing being
// reached, so an omitted namespace means the target's own scope and then its
// parents — a cache in /local guarded by `keeper` means the keeper /local can see,
// whoever is reaching it and from wherever.
func TestAnOmittedGuardNamespaceWalksOutFromTheTarget(t *testing.T) {
	proj := guardedProject(t, map[string]any{"name": "keeper"})

	grant := guardOf(t, proj, []string{"local"}, "deep", nestedCache)
	require.Empty(t, grant.Refused, "the guard is in the parent and the walk goes outward")
	require.Equal(t, "keeper", grant.Settings["authorizer/name"])
	require.Equal(t, "/local", grant.Settings["authorizer/namespace"],
		"where the walk STARTED, which is what the tier needs to resolve it again")
}

// TestAGuardThatIsNotAnAuthorizerIsRefusedByName. A function implementing the
// handler world would instantiate and then be handed a question it reads as a
// request — the failure `implements` exists to prevent.
func TestAGuardThatImplementsTheHandlerIsRefused(t *testing.T) {
	proj := guardedProject(t, map[string]any{"name": "plain"})

	grant := guardOf(t, proj, nil, "one", cacheOneID)
	require.Contains(t, grant.Refused, "plain")
	require.Contains(t, grant.Refused, "rather than an authorizer")
	require.False(t, grant.Allows("get"), "a refused grant allows nothing")
	require.Empty(t, grant.Settings["authorizer/name"], "and carries no guard to be asked")
}

// TestAGuardThatIsNotThereIsRefusedRatherThanIgnored. This is the one that
// matters most: a guard nobody can run must not read as a resource nobody
// guarded.
func TestAGuardThatResolvesToNothingIsRefused(t *testing.T) {
	proj := guardedProject(t, map[string]any{"name": "ghost"})

	grant := guardOf(t, proj, nil, "one", cacheOneID)
	require.Contains(t, grant.Refused, "ghost")
	require.False(t, grant.Allows("get"))
}

// TestAnUnguardedTargetCarriesNothing, so the settings stay a statement that
// somebody decides rather than a key every grant has.
func TestAnUnguardedTargetCarriesNoGuard(t *testing.T) {
	proj := guardedProject(t, nil)

	grant := guardOf(t, proj, nil, "one", cacheOneID)
	require.Empty(t, grant.Refused)
	require.NotContains(t, grant.Settings, "authorizer/name")
}

// TestAnAlreadyRefusedGrantKeepsItsFirstSentence. A cache this cloud cannot
// serve at all, that also names a guard, should say the thing an author has to
// fix rather than burying it under a second sentence about the guard.
func TestAGuardDoesNotOverwriteAnExistingRefusal(t *testing.T) {
	proj := guardedProject(t, map[string]any{"name": "ghost"})
	target, _, err := proj.Resource(nil, "cache", "one")
	require.NoError(t, err)

	grant := invoke.Grant{Kind: "cache", Label: "one", Ops: []string{"get"}, Refused: "already unservable"}
	guardedBy(proj, Target{Project: "shop", Environment: "prod"}, target, cacheOneID, &grant)
	require.Equal(t, "already unservable", grant.Refused)
}
