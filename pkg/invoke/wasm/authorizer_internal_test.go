package wasm

import (
	"testing"

	"github.com/samyfodil/wazy/component"
	"github.com/stretchr/testify/require"
)

// WHAT CAME BACK WAS PRODUCED BY SOMEBODY ELSE'S COMPONENT, so every shape that
// is not this world's has to be an error naming the authorizer rather than a
// panic three frames later — and, more importantly, rather than a GRANT. A
// malformed answer read as permission is the one failure mode this whole
// control exists to prevent.
//
// The happy paths are proven against the real fixture in authorizer_test.go.
// These are the ones a real component cannot produce, which is exactly why they
// need a unit test: a guard that has been tampered with, or built against a
// world that has since moved, produces precisely these.
func TestAMalformedDecisionIsNeverAGrant(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value component.Value
	}{
		{"not a variant at all", "granted"},
		{"a bare list", []component.Value{"get"}},
		{"nil", nil},
		{"an arm this world does not have", &component.VariantValue{Disc: 7, Payload: "get"}},
		{"granted something that is not a list", &component.VariantValue{Disc: decisionGranted, Payload: "get"}},
		{"granted a list of not-strings", &component.VariantValue{
			Disc: decisionGranted, Payload: []component.Value{uint32(1)}}},
		{"undecided without a reason", &component.VariantValue{
			Disc: decisionUndecided, Payload: []component.Value{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := decisionFrom(tc.value, "keeper")
			require.ErrorIs(t, err, ErrNotAnAuthorizer)
			require.Contains(t, err.Error(), "keeper", "the sentence names which guard")
			require.Empty(t, decision.Granted, "a shape nobody understands grants nothing")
		})
	}
}

// TestAnEmptyGrantArrivesAsNIL and must not be read as malformed — it is the
// commonest refusal there is. The fixture proves this end to end; this pins the
// lifting rule on its own, because the two are easy to conflate when reading
// decisionFrom and only one of them is a bug.
func TestAGrantOfNothingIsStillAGrant(t *testing.T) {
	decision, err := decisionFrom(&component.VariantValue{Disc: decisionGranted, Payload: nil}, "keeper")
	require.NoError(t, err)
	require.NotNil(t, decision.Granted)
	require.Empty(t, decision.Granted)
	require.Empty(t, decision.Undecided)
}

// TestAVariantArrivesEitherWay. wazy hands back a pointer today; a value is the
// same answer and lifting must not depend on which.
func TestADecisionLiftsByValueOrPointer(t *testing.T) {
	want := []string{"get", "put"}
	payload := []component.Value{"get", "put"}

	byPointer, err := decisionFrom(&component.VariantValue{Disc: decisionGranted, Payload: payload}, "keeper")
	require.NoError(t, err)
	require.Equal(t, want, byPointer.Granted)

	byValue, err := decisionFrom(component.VariantValue{Disc: decisionGranted, Payload: payload}, "keeper")
	require.NoError(t, err)
	require.Equal(t, want, byValue.Granted)
}

// TestTheQuestionLowersInTheWITsOWNORDER. questionValue is the one place the
// field order in the world file is load-bearing, and a transposition there is
// silent: two strings swap and everything still typechecks. The fixture catches
// it end to end; this says which position each field is in, so a reader of the
// WIT can check it against this without running anything.
func TestTheQuestionLowersInFieldOrder(t *testing.T) {
	value := questionValue(Question{
		Who:  Caller{Project: "p", Environment: "e", Kind: "k", Name: "n", Scope: []string{"a", "b"}},
		What: Target{Kind: "tk", Label: "tl", Instance: "ti"},
		Op:   "get",
	})

	fields, ok := value.([]component.Value)
	require.True(t, ok)
	require.Len(t, fields, 3, "question is who, what, op")

	who, ok := fields[0].([]component.Value)
	require.True(t, ok)
	require.Equal(t, []component.Value{"p", "e", "k", "n", []component.Value{"a", "b"}}, who)

	what, ok := fields[1].([]component.Value)
	require.True(t, ok)
	require.Equal(t, []component.Value{"tk", "tl", "ti"}, what)

	require.Equal(t, "get", fields[2])
}

// TestAnEmptySCOPEISANEMPTYLISTANDNOTANULL — the world says list<string>, which
// has no absent case, and a nil there traps rather than lowering.
func TestACallerAtTheRootLowersAnEmptyScope(t *testing.T) {
	fields := questionValue(Question{}).([]component.Value)
	who := fields[0].([]component.Value)
	require.NotNil(t, who[4])
	require.Empty(t, who[4])
}
