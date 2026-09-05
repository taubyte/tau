package node

import (
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

// admitting is a node holding the validator's project, and the Admitter over it.
func admittingNode(t *testing.T, answers *answering) (*Node, *admissions) {
	t.Helper()

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRuntimes(answers))
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withValidator(),
	})
	require.NoError(t, err)

	return n, &admissions{node: n}
}

// TestTHEVALIDATORIsInvoked, and it is told which instance and which store — a
// workload deciding for two stores has nothing else to tell them apart by.
func TestAdmittingInvokesTheValidator(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, a := admittingNode(t, answers)

	require.NoError(t, a.Admit(t.Context(), manyGrant("0"), "room-42"))

	seen := answers.seen()
	require.Len(t, seen, 1)
	require.Equal(t, "POST", seen[0].Method)
	require.Equal(t, []string{"room-42"}, seen[0].Headers[headerInstance])
	require.Equal(t, []string{"sessions"}, seen[0].Headers[headerFor])
}

// TestAVALIDATORThatSaysNoIsAREFUSALAndNotAFailure: the target declared who
// decides, and this is that decision.
func TestAValidatorThatRefuses(t *testing.T) {
	answers := &answering{name: "wasm", status: 403}
	_, a := admittingNode(t, answers)

	err := a.Admit(t.Context(), manyGrant("0"), "room-42")
	require.ErrorIs(t, err, wasmtier.ErrNotAdmitted)
	require.ErrorContains(t, err, "may_open")
}

// TestTHEDECISIONIsRecorded, so a validator is asked once per instance rather
// than once per call — which matters because a validator may have side effects.
func TestAnAdmittedInstanceIsNotAskedAgain(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, a := admittingNode(t, answers)

	for range 3 {
		require.NoError(t, a.Admit(t.Context(), manyGrant("0"), "room-42"))
	}
	require.Len(t, answers.seen(), 1, "the validator was asked more than once")

	// AND THE RECORD IS THE AUTHORITY RATHER THAN THIS NODE'S MEMORY: a second
	// Admitter over the same node reads it and asks nobody.
	fresh := &admissions{node: n}
	require.NoError(t, fresh.Admit(t.Context(), manyGrant("0"), "room-42"))
	require.Len(t, answers.seen(), 1)
}

// TestTHECEILINGHolds, and refusing at it is the same arm a validator's no is —
// both are "this instance may not exist".
func TestTheLimitRefusesFurtherInstances(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, a := admittingNode(t, answers)

	require.NoError(t, a.Admit(t.Context(), manyGrant("2"), "a"))
	require.NoError(t, a.Admit(t.Context(), manyGrant("2"), "b"))

	err := a.Admit(t.Context(), manyGrant("2"), "c")
	require.ErrorIs(t, err, wasmtier.ErrNotAdmitted)
	require.ErrorContains(t, err, "limit is 2")

	// AND ONE ALREADY ADMITTED IS STILL ADMITTED. A full store still serves
	// what is in it.
	require.NoError(t, a.Admit(t.Context(), manyGrant("2"), "a"))
}

// TestALIMITOfZeroIsNoCeiling, which the vocabulary makes a required sentence
// rather than a default nobody reached.
func TestALimitOfZeroAdmitsAnything(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, a := admittingNode(t, answers)

	for _, name := range []string{"a", "b", "c", "d"} {
		require.NoError(t, a.Admit(t.Context(), manyGrant("0"), name))
	}
}

// TestTWOSTORESDoNotShareACeiling, because the count is scoped by handle the
// way everything else is.
func TestInstancesAreCountedPerStore(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, a := admittingNode(t, answers)

	other := manyGrant("1")
	other.Handle = "shop/prod/QmOther"

	require.NoError(t, a.Admit(t.Context(), manyGrant("1"), "only"))
	require.NoError(t, a.Admit(t.Context(), other, "only"),
		"a second store's first instance was counted against the first store")
}

// TestAVALIDATORThatCouldNotBeRunIsNotAREFUSAL. Admitting anyway would grant
// what the target said somebody else decides; saying "not admitted" would tell
// the caller their instance was turned down, which is a different thing to act
// on.
func TestAValidatorThatCannotRunIsNotARefusal(t *testing.T) {
	answers := &answering{name: "wasm", fail: true}
	_, a := admittingNode(t, answers)

	err := a.Admit(t.Context(), manyGrant("0"), "room")
	require.Error(t, err)
	require.NotErrorIs(t, err, wasmtier.ErrNotAdmitted)
	require.ErrorContains(t, err, "could not be run")
}

// TestASTOREThatNamesNoValidatorIsRefused, which is a compiled object the
// vocabulary does not allow — the block's fields are required together.
func TestAStoreWithNoValidatorIsRefused(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, a := admittingNode(t, answers)

	grant := manyGrant("0")
	delete(grant.Settings, settingInstanceValidator)

	require.ErrorIs(t, a.Admit(t.Context(), grant, "room"), ErrNoValidator)
}

// TestAHANDLEThatNamesNoDeploymentIsRefused rather than invoking something in
// whatever project an empty name resolves to.
func TestAMalformedHandleIsNotInvoked(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, a := admittingNode(t, answers)

	grant := manyGrant("0")
	grant.Handle = "nonsense"

	err := a.Admit(t.Context(), grant, "room")
	require.Error(t, err)
	require.ErrorContains(t, err, "does not name a deployment")
	require.Empty(t, answers.seen())
}
