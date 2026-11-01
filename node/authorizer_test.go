package node

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// The three policy choices this file pins: an answer is kept per caller and target,
// the key carries the caller's revision, and an undecided answer is not kept.

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

func TestANewRevisionIsANewQuestion(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker}})

	a := &authorizers{node: n}
	grant := guardedGrant()
	a.remember(keyFor(caller("r1"), grant), []string{"get", "put"})

	_, err := a.Ops(t.Context(), caller("r2"), grant, "get")
	require.Error(t, err, "the old answer must not be reused for new code")
	require.Contains(t, err.Error(), "cannot run one", "it fell through to asking")
}

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

func TestANodeWithNoTierCannotAnswer(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker}})

	_, err := (&authorizers{node: n}).Ops(t.Context(), caller("r1"), guardedGrant(), "get")
	require.Error(t, err)
	require.Contains(t, err.Error(), "keeper", "and names the guard it could not run")
}

func TestAGuardedHandleMustNameADeployment(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker}})
	a := &authorizers{node: n}
	a.node.decider = nil

	grant := guardedGrant()
	grant.Handle = "nonsense"

	_, err := a.Ops(t.Context(), caller("r1"), grant, "get")
	require.Error(t, err)
}

func TestTheInstanceIsReadOffTheHandle(t *testing.T) {
	require.Equal(t, "room-42", instanceOf("shop/prod/QmChat/room-42"))
	require.Empty(t, instanceOf("shop/prod/QmChat"), "a store that holds one has none")
	require.Empty(t, instanceOf("nonsense"))
}

const validatorID = "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"

// withValidator is a project whose one function is the control a target names —
// a store's instance validator, or a route's guard. It is the SAME function
// either way, which is the property both of those resolve paths rest on.
func withValidator() []byte {
	return []byte(`{
		"object": {
			"id": "` + projectID + `",
			"name": "shop",
			"function": {
				"` + validatorID + `": {
					"name": "may_open",
					"runtime": "wasm",
					"execution": {"call": "handle", "memory": 33554432},
					"source": "services/may_open"
				}
			}
		},
		"indexes": {
			"projects/` + projectID + `/function/may_open/links": [
				"branches/main/projects/` + projectID + `/function/` + validatorID + `"]
		}
	}`)
}

// guarding stands in for the component tier here the way node/admit's own double
// does for a validator: what a REAL guard does with a real question is
// pkg/invoke/wasm's own test, and what this proves is the node's policy around it.
type guarding struct {
	decision wasmtier.Decision
	err      error

	mu    sync.Mutex
	asked []wasmtier.Question
	specs []string
}

func (g *guarding) Authorize(_ context.Context, spec invoke.Spec, q wasmtier.Question) (wasmtier.Decision, error) {
	g.mu.Lock()
	g.asked = append(g.asked, q)
	g.specs = append(g.specs, spec.Name)
	g.mu.Unlock()
	return g.decision, g.err
}

func (g *guarding) Resolve(context.Context, invoke.Spec, invoke.Question) (*invoke.Resolution, error) {
	return nil, errors.New("not asked here")
}

func (g *guarding) Admit(context.Context, invoke.Spec, wasmtier.Opening) (wasmtier.Verdict, error) {
	return wasmtier.Verdict{}, errors.New("not asked here")
}

func (g *guarding) questions() []wasmtier.Question {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]wasmtier.Question(nil), g.asked...)
}

// guardingNode is a node holding the guard's project, with the tier's answer
// faked. The guard is the same `may_open` function the admission fixture
// publishes — a guard is resolved like any other function, which is the point.
func guardingNode(t *testing.T, answers *guarding) (*Node, *authorizers) {
	t.Helper()

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery}},
		WithRuntimes(&answering{name: "wasm", status: 200}), prebuilt())
	require.NoError(t, n.Start(t.Context()))
	n.decider = answers

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withValidator(),
	})
	require.NoError(t, err)

	return n, &authorizers{node: n}
}

// guardedBy is a grant whose target names a guard at the project root.
func guardedBy(name string) invoke.Grant {
	return invoke.Grant{
		Kind: "cache", Label: "orders", Handle: "shop/prod/QmOrders/tenant-7",
		Ops:      []string{"get", "put", "delete"},
		Settings: map[string]string{settingAuthorizer: name},
	}
}

func TestTheGuardIsAskedAboutTheCallerAndTheTarget(t *testing.T) {
	answers := &guarding{decision: wasmtier.Decision{Granted: []string{"get"}}}
	_, a := guardingNode(t, answers)

	ops, err := a.Ops(t.Context(), caller("r1"), guardedBy("may_open"), "get")
	require.NoError(t, err)
	require.Equal(t, []string{"get"}, ops)

	asked := answers.questions()
	require.Len(t, asked, 1)
	require.Equal(t, "checkout", asked[0].Who.Name, "who is asking")
	require.Equal(t, "function", asked[0].Who.Kind)
	require.Equal(t, "orders", asked[0].What.Label, "what the CALLER wrote in its access block")
	require.Equal(t, "cache", asked[0].What.Kind)
	require.Equal(t, "tenant-7", asked[0].What.Instance, "and which instance of it")
	require.Equal(t, "get", asked[0].Op)

	// AND THE ANSWER IS KEPT per caller and target, which is what keeps a wasm
	// instantiation off a cache's hot path.
	ops, err = a.Ops(t.Context(), caller("r1"), guardedBy("may_open"), "get")
	require.NoError(t, err)
	require.Equal(t, []string{"get"}, ops)
	require.Len(t, answers.questions(), 1, "the second call was answered from the cache")
}

func TestAGuardThatDoesNotDecideIsNotKept(t *testing.T) {
	answers := &guarding{decision: wasmtier.Decision{Undecided: "the plan service did not answer"}}
	_, a := guardingNode(t, answers)

	_, err := a.Ops(t.Context(), caller("r1"), guardedBy("may_open"), "get")
	require.ErrorIs(t, err, wasmtier.ErrUndecided)
	require.ErrorContains(t, err, "the plan service did not answer")

	_, err = a.Ops(t.Context(), caller("r1"), guardedBy("may_open"), "get")
	require.Error(t, err)
	require.Len(t, answers.questions(), 2, "nothing was remembered, so it was asked again")
}

func TestAGuardThatFailsIsNotADecision(t *testing.T) {
	answers := &guarding{err: errors.New("the component trapped")}
	_, a := guardingNode(t, answers)

	_, err := a.Ops(t.Context(), caller("r1"), guardedBy("may_open"), "get")
	require.ErrorContains(t, err, "asking the authorizer")
	require.ErrorContains(t, err, "the component trapped")
}

func TestAGuardThatIsNotInTheProjectIsReported(t *testing.T) {
	answers := &guarding{decision: wasmtier.Decision{Granted: []string{"get"}}}
	_, a := guardingNode(t, answers)

	_, err := a.Ops(t.Context(), caller("r1"), guardedBy("nobody_wrote_this"), "get")
	require.ErrorContains(t, err, "resolving the authorizer")
	require.ErrorContains(t, err, "nobody_wrote_this")
	require.Empty(t, answers.questions(), "nothing was asked, because there was nothing to ask")
}

func TestAnUnguardedGrantKeepsItsOwnOps(t *testing.T) {
	answers := &guarding{}
	_, a := guardingNode(t, answers)

	grant := guardedBy("may_open")
	delete(grant.Settings, settingAuthorizer)

	ops, err := a.Ops(t.Context(), caller("r1"), grant, "get")
	require.NoError(t, err)
	require.Equal(t, []string{"get", "put", "delete"}, ops)
	require.Empty(t, answers.questions())
}

// AuthorizeRequest is the third decision a tier makes. These doubles exist for
// the other two, so it answers the way an absent guard would.
func (*guarding) AuthorizeRequest(context.Context, invoke.Spec, *invoke.Request) (*invoke.Admission, error) {
	return &invoke.Admission{Granted: []string{}}, nil
}
