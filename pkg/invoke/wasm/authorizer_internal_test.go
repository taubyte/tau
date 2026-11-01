package wasm

import (
	"testing"

	"github.com/samyfodil/wazy/component"
	"github.com/stretchr/testify/require"
)

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

func TestAGrantOfNothingIsStillAGrant(t *testing.T) {
	decision, err := decisionFrom(&component.VariantValue{Disc: decisionGranted, Payload: nil}, "keeper")
	require.NoError(t, err)
	require.NotNil(t, decision.Granted)
	require.Empty(t, decision.Granted)
	require.Empty(t, decision.Undecided)
}

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

func TestACallerAtTheRootLowersAnEmptyScope(t *testing.T) {
	fields := questionValue(Question{}).([]component.Value)
	who := fields[0].([]component.Value)
	require.NotNil(t, who[4])
	require.Empty(t, who[4])
}
