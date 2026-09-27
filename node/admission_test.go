package node

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// admission_test is the half only the node can do: the validator a target names
// is a WORKLOAD, so admitting an instance means invoking one — and the decision
// has to be recorded, because putting an invocation in front of every get is a
// store nobody would use.

const validatorID = "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"

// withValidator is a project whose one function is the validator a store names.
func withValidator() []byte {
	return []byte(`{
		"object": {
			"id": "` + projectID + `",
			"name": "shop",
			"function": {
				"` + validatorID + `": {
					"name": "may_open",
					"execution": {"call": "handle", "memory": 33554432},
					"source": {"kind": "component", "component": "QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv"}
				}
			}
		},
		"indexes": {
			"projects/` + projectID + `/function/may_open/links": [
				"branches/main/projects/` + projectID + `/function/` + validatorID + `"]
		}
	}`)
}

// manyGrant is a store that holds many instances, as the composer writes one.
func manyGrant(limit string) invoke.Grant {
	return invoke.Grant{
		Kind: "cache", Label: "sessions", Handle: "shop/prod/QmSessions",
		Ops: []string{"get", "put"},
		Settings: map[string]string{
			settingInstanceAdmission: "validator",
			settingInstanceValidator: "may_open",
			settingInstanceLimit:     limit,
		},
	}
}

// deciding records what a validator was asked and answers with what it was
// told to. It stands in for the component tier, not for the validator: what a
// REAL validator does with a real opening is proven against fixtures/admitter in
// pkg/invoke/wasm and end to end in node/dream.
type deciding struct {
	verdict wasmtier.Verdict
	err     error

	mu   sync.Mutex
	saw  []wasmtier.Opening
	spec []string
}

func (d *deciding) Authorize(context.Context, invoke.Spec, wasmtier.Question) (wasmtier.Decision, error) {
	return wasmtier.Decision{}, errors.New("not asked here")
}

func (d *deciding) Resolve(context.Context, invoke.Spec, invoke.Question) (*invoke.Resolution, error) {
	return nil, errors.New("not asked here")
}

func (d *deciding) Admit(_ context.Context, spec invoke.Spec, o wasmtier.Opening) (wasmtier.Verdict, error) {
	d.mu.Lock()
	d.saw = append(d.saw, o)
	d.spec = append(d.spec, spec.Name)
	d.mu.Unlock()
	return d.verdict, d.err
}

func (d *deciding) seen() []wasmtier.Opening {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]wasmtier.Opening(nil), d.saw...)
}

// admittingNode is a node holding the validator's project, with the tier's
// decision faked so the node's own policy is what is under test.
func admittingNode(t *testing.T, decides *deciding) (*Node, *admissions) {
	t.Helper()

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRuntimes(&answering{name: "wasm", status: 200}))
	require.NoError(t, n.Start(t.Context()))
	n.decider = decides

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withValidator(),
	})
	require.NoError(t, err)

	return n, &admissions{node: n}
}

// TestTHEVALIDATORIsInvoked, and it is told which instance and which store — a
// workload deciding for two stores has nothing else to tell them apart by.
func TestAdmittingInvokesTheValidator(t *testing.T) {
	answers := &deciding{verdict: wasmtier.Verdict{Admitted: true}}
	_, a := admittingNode(t, answers)

	caller := invoke.Spec{Project: "shop", Environment: "prod", Kind: "function", Label: "chat"}
	require.NoError(t, a.Admit(t.Context(), caller, manyGrant("0"), "room-42"))

	seen := answers.seen()
	require.Len(t, seen, 1)
	require.Equal(t, "room-42", seen[0].Instance)
	require.Equal(t, "sessions", seen[0].What.Label, "a workload deciding for two stores has nothing else to tell them apart by")
	require.Equal(t, "cache", seen[0].What.Kind)
	require.Equal(t, "chat", seen[0].Who.Name, "and who was asking")
}

// TestAVALIDATORThatSaysNoIsAREFUSALAndNotAFailure: the target declared who
// decides, and this is that decision.
func TestAValidatorThatRefuses(t *testing.T) {
	answers := &deciding{verdict: wasmtier.Verdict{Refused: "over its plan"}}
	_, a := admittingNode(t, answers)

	err := a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room-42")
	require.ErrorIs(t, err, wasmtier.ErrNotAdmitted)
	require.ErrorContains(t, err, "may_open")
	require.ErrorContains(t, err, "over its plan", "the validator's own words reach the caller")
}

// TestUNDECIDEDISNOTAREFUSAL, which a status code had no room to say. Telling a
// caller their instance was turned down when nothing decided sends them to argue
// with a policy that never fired.
func TestAValidatorThatDoesNotDecideIsNotARefusal(t *testing.T) {
	answers := &deciding{verdict: wasmtier.Verdict{Undecided: "the plan service did not answer"}}
	_, a := admittingNode(t, answers)

	err := a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room-42")
	require.Error(t, err)
	require.NotErrorIs(t, err, wasmtier.ErrNotAdmitted, "nothing decided, so nobody was turned down")
	require.ErrorContains(t, err, "did not decide")
}

// TestTHEDECISIONIsRecorded, so a validator is asked once per instance rather
// than once per call — which matters because a validator may have side effects.
func TestAnAdmittedInstanceIsNotAskedAgain(t *testing.T) {
	answers := &deciding{verdict: wasmtier.Verdict{Admitted: true}}
	n, a := admittingNode(t, answers)

	for range 3 {
		require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room-42"))
	}
	require.Len(t, answers.seen(), 1, "the validator was asked more than once")

	// AND THE RECORD IS THE AUTHORITY RATHER THAN THIS NODE'S MEMORY: a second
	// Admitter over the same node reads it and asks nobody.
	fresh := &admissions{node: n}
	require.NoError(t, fresh.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room-42"))
	require.Len(t, answers.seen(), 1)
}

// TestTHECEILINGHolds, and refusing at it is the same arm a validator's no is —
// both are "this instance may not exist".
func TestTheLimitRefusesFurtherInstances(t *testing.T) {
	answers := &deciding{verdict: wasmtier.Verdict{Admitted: true}}
	_, a := admittingNode(t, answers)

	require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("2"), "a"))
	require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("2"), "b"))

	err := a.Admit(t.Context(), invoke.Spec{}, manyGrant("2"), "c")
	require.ErrorIs(t, err, wasmtier.ErrNotAdmitted)
	require.ErrorContains(t, err, "limit is 2")

	// AND ONE ALREADY ADMITTED IS STILL ADMITTED. A full store still serves
	// what is in it.
	require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("2"), "a"))
}

// TestALIMITOfZeroIsNoCeiling, which the vocabulary makes a required sentence
// rather than a default nobody reached.
func TestALimitOfZeroAdmitsAnything(t *testing.T) {
	answers := &deciding{verdict: wasmtier.Verdict{Admitted: true}}
	_, a := admittingNode(t, answers)

	for _, name := range []string{"a", "b", "c", "d"} {
		require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), name))
	}
}

// TestTWOSTORESDoNotShareACeiling, because the count is scoped by handle the
// way everything else is.
func TestInstancesAreCountedPerStore(t *testing.T) {
	answers := &deciding{verdict: wasmtier.Verdict{Admitted: true}}
	_, a := admittingNode(t, answers)

	other := manyGrant("1")
	other.Handle = "shop/prod/QmOther"

	require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("1"), "only"))
	require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, other, "only"),
		"a second store's first instance was counted against the first store")
}

// TestAVALIDATORThatCouldNotBeRunIsNotAREFUSAL. Admitting anyway would grant
// what the target said somebody else decides; saying "not admitted" would tell
// the caller their instance was turned down, which is a different thing to act
// on.
func TestAValidatorThatCannotRunIsNotARefusal(t *testing.T) {
	answers := &deciding{err: errors.New("the component would not start")}
	_, a := admittingNode(t, answers)

	err := a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room")
	require.Error(t, err)
	require.NotErrorIs(t, err, wasmtier.ErrNotAdmitted)
	require.ErrorContains(t, err, "could not be run")
}

// TestASTOREThatNamesNoValidatorIsRefused, which is a compiled object the
// vocabulary does not allow — the block's fields are required together.
func TestAStoreWithNoValidatorIsRefused(t *testing.T) {
	answers := &deciding{verdict: wasmtier.Verdict{Admitted: true}}
	_, a := admittingNode(t, answers)

	grant := manyGrant("0")
	delete(grant.Settings, settingInstanceValidator)

	require.ErrorIs(t, a.Admit(t.Context(), invoke.Spec{}, grant, "room"), ErrNoValidator)
}

// TestAHANDLEThatNamesNoDeploymentIsRefused rather than invoking something in
// whatever project an empty name resolves to.
func TestAMalformedHandleIsNotInvoked(t *testing.T) {
	answers := &deciding{verdict: wasmtier.Verdict{Admitted: true}}
	_, a := admittingNode(t, answers)

	grant := manyGrant("0")
	grant.Handle = "nonsense"

	err := a.Admit(t.Context(), invoke.Spec{}, grant, "room")
	require.Error(t, err)
	require.ErrorContains(t, err, "does not name a deployment")
	require.Empty(t, answers.seen())
}

// TestABrowserAndAWorkloadAskTheSameValidator. A workload opening
// `chat/room-42` and a browser attaching to `/room-42` are the same question
// about the same instance, and two answers to it would be two security policies
// for one config block.
func TestABrowserAndAWorkloadAskTheSameValidator(t *testing.T) {
	answers := &deciding{verdict: wasmtier.Verdict{Admitted: true}}
	_, a := admittingNode(t, answers)

	require.NoError(t, a.AdmitInstance(t.Context(), "shop/prod/QmSessions",
		manyGrant("0").Settings, "room-42"))

	seen := answers.seen()
	require.Len(t, seen, 1)
	require.Equal(t, "room-42", seen[0].Instance)

	// A BROWSER IS NOT A WORKLOAD AND SAYS SO BY ITS KIND BEING EMPTY. The
	// deployment it is attaching to is known — it is in the handle — and
	// nothing else about the caller is, because it came from the internet.
	require.Equal(t, "shop", seen[0].Who.Project)
	require.Equal(t, "prod", seen[0].Who.Environment)
	require.Empty(t, seen[0].Who.Kind)
	require.Empty(t, seen[0].Who.Name)

	// The store is named by the id, because a browser never saw the author's
	// label and the id is better than nothing in a log.
	require.Equal(t, "QmSessions", seen[0].What.Label)

	// AND THE CEILING IS THE SAME ONE. A browser admitted here is an instance
	// held, so a workload asking next is asking about an instance that exists.
	require.NoError(t, a.Admit(t.Context(),
		invoke.Spec{Project: "shop", Environment: "prod"}, manyGrant("0"), "room-42"))
	require.Len(t, answers.seen(), 1, "the same instance was not validated twice")
}

// TestABrowserTurnedAwayIsARefusal, reaching the browser as the validator's own
// words — the same string a workload would get.
func TestABrowserTurnedAwayIsARefusal(t *testing.T) {
	answers := &deciding{verdict: wasmtier.Verdict{Refused: "that room is full"}}
	_, a := admittingNode(t, answers)

	err := a.AdmitInstance(t.Context(), "shop/prod/QmSessions", manyGrant("0").Settings, "room-42")
	require.ErrorIs(t, err, wasmtier.ErrNotAdmitted)
	require.ErrorContains(t, err, "that room is full")
}

// AuthorizeRequest is the third decision a tier makes. These doubles exist for
// the other two, so it answers the way an absent guard would.
func (*deciding) AuthorizeRequest(context.Context, invoke.Spec, *invoke.Request) (*invoke.Admission, error) {
	return &invoke.Admission{Granted: []string{}}, nil
}
