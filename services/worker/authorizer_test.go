package worker

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

// Guard resolution against hand-built projects. The compiler already refuses a
// guard that is not an authorizer (checks.go), so the fixture cannot carry these
// cases; this tests the second line of defence, a compiled object not from tcc.

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
					"runtime":   "wasm",
					"execution": map[string]any{"call": "authorize", "memory": 8388608},
				},
				plainFuncID: map[string]any{
					"name": "plain", "implements": "handler",
					"runtime":   "wasm",
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
							// No namespace written, so the guard must be found in the parent.
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

func TestAGuardResolvesToAnAuthorizer(t *testing.T) {
	proj := guardedProject(t, map[string]any{"name": "keeper"})

	grant := guardOf(t, proj, nil, "one", cacheOneID)
	require.Empty(t, grant.Refused)
	require.Equal(t, "keeper", grant.Settings["authorizer/name"])
	require.Equal(t, "/", grant.Settings["authorizer/namespace"],
		"the project root, stated rather than left empty for a reader to guess")
}

func TestAnOmittedGuardNamespaceWalksOutFromTheTarget(t *testing.T) {
	proj := guardedProject(t, map[string]any{"name": "keeper"})

	grant := guardOf(t, proj, []string{"local"}, "deep", nestedCache)
	require.Empty(t, grant.Refused, "the guard is in the parent and the walk goes outward")
	require.Equal(t, "keeper", grant.Settings["authorizer/name"])
	require.Equal(t, "/local", grant.Settings["authorizer/namespace"],
		"where the walk STARTED, which is what the tier needs to resolve it again")
}

func TestAGuardThatImplementsTheHandlerIsRefused(t *testing.T) {
	proj := guardedProject(t, map[string]any{"name": "plain"})

	grant := guardOf(t, proj, nil, "one", cacheOneID)
	require.Contains(t, grant.Refused, "plain")
	require.Contains(t, grant.Refused, "rather than an authorizer")
	require.False(t, grant.Allows("get"), "a refused grant allows nothing")
	require.Empty(t, grant.Settings["authorizer/name"], "and carries no guard to be asked")
}

func TestAGuardThatResolvesToNothingIsRefused(t *testing.T) {
	proj := guardedProject(t, map[string]any{"name": "ghost"})

	grant := guardOf(t, proj, nil, "one", cacheOneID)
	require.Contains(t, grant.Refused, "ghost")
	require.False(t, grant.Allows("get"))
}

func TestAnUnguardedTargetCarriesNoGuard(t *testing.T) {
	proj := guardedProject(t, nil)

	grant := guardOf(t, proj, nil, "one", cacheOneID)
	require.Empty(t, grant.Refused)
	require.NotContains(t, grant.Settings, "authorizer/name")
}

func TestAGuardDoesNotOverwriteAnExistingRefusal(t *testing.T) {
	proj := guardedProject(t, map[string]any{"name": "ghost"})
	target, _, err := proj.Resource(nil, "cache", "one")
	require.NoError(t, err)

	grant := invoke.Grant{Kind: "cache", Label: "one", Ops: []string{"get"}, Refused: "already unservable"}
	guardedBy(proj, Target{Project: "shop", Environment: "prod"}, target, cacheOneID, &grant)
	require.Equal(t, "already unservable", grant.Refused)
}
