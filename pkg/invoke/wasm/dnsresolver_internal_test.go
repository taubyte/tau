package wasm

import (
	"testing"

	"github.com/samyfodil/wazy/component"
	"github.com/stretchr/testify/require"
)

// WHAT CAME BACK WAS PRODUCED BY SOMEBODY ELSE'S COMPONENT, and for DNS a
// malformed answer is worse than a malformed grant. An arm this world does not
// have, read as `absent`, is NXDOMAIN — which is cacheable AS AN ABSENCE, so one
// broken resolver would teach every resolver that asked that the name does not
// exist, and keep teaching it for the negative TTL.
//
// The happy paths are proven against the real fixture in dnsresolver_test.go.
// These are the ones a real component cannot produce, which is exactly why they
// need a unit test: a resolver built against a world that has since moved
// produces precisely these.
func TestAMalformedAnswerIsNeverAnAnswer(t *testing.T) {
	for _, one := range []struct {
		name  string
		value component.Value
		says  string
	}{
		{
			name:  "not a variant at all",
			value: "geo 60 IN A 203.0.113.9",
			says:  "rather than an answer",
		},
		{
			name:  "an arm this world does not have",
			value: &component.VariantValue{Disc: 7},
			says:  "arm 7",
		},
		{
			name:  "records that are not a list",
			value: &component.VariantValue{Disc: answerRecords, Payload: "geo 60 IN A 203.0.113.9"},
			says:  "rather than a list",
		},
		{
			name: "a record that is not a string",
			value: &component.VariantValue{Disc: answerRecords, Payload: []component.Value{
				"geo 60 IN A 203.0.113.9", uint32(42),
			}},
			says: "a record that is not a string",
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			got, err := resolutionFrom(one.value, "warren")
			require.Nil(t, got, "a shape this world does not have is not an answer")
			require.ErrorIs(t, err, ErrNotADNSResolver)
			require.Contains(t, err.Error(), "warren", "naming the resolver, for an operator")
			require.Contains(t, err.Error(), one.says)
		})
	}
}

// TestAVARIANTBYVALUEISLIFTEDTOO. wazy may hand back either, and a resolver
// whose answer arrived by value rather than by pointer has still answered.
func TestAnAnswerByValueIsLifted(t *testing.T) {
	got, err := resolutionFrom(component.VariantValue{Disc: answerAbsent}, "warren")
	require.NoError(t, err)
	require.False(t, got.Exists)
}

// TestANILPAYLOADONRECORDSISEMPTYANDNOTMALFORMED. The world says an empty list
// in `records` is not a refusal, and an empty list arrives as a nil payload —
// reading that as a bad shape would turn "this name holds nothing of that type"
// into an author's bug, and the caller would then get an error where it should
// get NOERROR.
func TestAnEmptyRecordListIsNotMalformed(t *testing.T) {
	got, err := resolutionFrom(&component.VariantValue{Disc: answerRecords, Payload: nil}, "warren")
	require.NoError(t, err)
	require.True(t, got.Exists, "the name is here")
	require.Empty(t, got.Records, "and holds nothing of the type asked for")
}
