package fleet_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/fleet"
)

func TestADocumentRoundTrips(t *testing.T) {
	raw, err := fleet.Encode(fleet.Fleet{Replication: 3})
	require.NoError(t, err)

	back, err := fleet.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, 3, back.Replication)
}

// TestACLOUDTHATSAIDNOTHINGREADSASZERO, which the reader turns into its own
// default — and every reader reaches the same one, so they still agree.
func TestAnEmptyDocumentIsZeroAndNotAnError(t *testing.T) {
	back, err := fleet.Decode([]byte(`{}`))
	require.NoError(t, err)
	require.Zero(t, back.Replication)
}

// TestAMALFORMEDDOCUMENTISANERROR AND NEVER A DEFAULT. A fleet whose
// declaration is corrupt has not said nothing, and reading it as silence would
// have every node quietly pick its own floor — the disagreement this document
// exists to end.
func TestAMalformedDocumentIsRefused(t *testing.T) {
	for _, bad := range [][]byte{
		[]byte("{not json"),
		[]byte(`{"replication":-1}`),
		nil,
	} {
		_, err := fleet.Decode(bad)
		require.ErrorIs(t, err, fleet.ErrMalformed, "%q", bad)
	}
}

// TestTHESTORAGEBLOCKROUNDTRIPS, and it is nested rather than spelled flat
// beside replication because Barrier <= Replicas is a relation between the two.
func TestTheStorageBlockRoundTrips(t *testing.T) {
	raw, err := fleet.Encode(fleet.Fleet{
		Replication: 3,
		Storage:     fleet.Storage{Replicas: 5, Barrier: 3},
	})
	require.NoError(t, err)

	back, err := fleet.Decode(raw)
	require.NoError(t, err)
	require.Equal(t, 3, back.Replication)
	require.Equal(t, 5, back.Storage.Replicas)
	require.Equal(t, 3, back.Storage.Barrier)
}

// TestACLOUDTHATSAIDNOTHINGABOUTOBJECTSSAYSSO. omitempty has never omitted a
// struct, so the tag is omitzero: an operator reading the document can tell a
// cloud that declared nothing about objects from one that did.
func TestAFleetThatDeclaresNothingAboutObjectsOmitsTheBlock(t *testing.T) {
	raw, err := fleet.Encode(fleet.Fleet{Replication: 3})
	require.NoError(t, err)
	require.Equal(t, `{"replication":3}`, string(raw))

	back, err := fleet.Decode(raw)
	require.NoError(t, err)
	require.Zero(t, back.Storage.Replicas)
	require.Zero(t, back.Storage.Barrier)
}

// TestZEROMEANSTHEREADERSDEFAULT, per field. A cloud that raised only the
// replica target still promises the reader's barrier, and every reader fills it
// the same way — so they still agree.
func TestResolvedFillsEachZeroSeparately(t *testing.T) {
	both, err := fleet.Storage{}.Resolved(3, 2)
	require.NoError(t, err)
	require.Equal(t, fleet.Storage{Replicas: 3, Barrier: 2}, both)

	onlyR, err := fleet.Storage{Replicas: 5}.Resolved(3, 2)
	require.NoError(t, err)
	require.Equal(t, fleet.Storage{Replicas: 5, Barrier: 2}, onlyR)

	onlyK, err := fleet.Storage{Barrier: 1}.Resolved(3, 2)
	require.NoError(t, err)
	require.Equal(t, fleet.Storage{Replicas: 3, Barrier: 1}, onlyK)
}

// TestAPAIRTHATCANNOTBECORRECTISREFUSED, and it has to be Resolved rather than
// Decode that refuses it: {Replicas: 0, Barrier: 5} is valid field by field and
// is only impossible once a reader says what its zero is.
//
// It wraps ErrMalformed so a reader needs no second rule — an impossible pair
// is refused exactly like a document that will not parse.
func TestAPairThatCannotBeCorrectIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		in   fleet.Storage
		says string
	}{
		{"barrier above replicas", fleet.Storage{Replicas: 2, Barrier: 3}, "every write would fail"},
		{"barrier above the default replicas", fleet.Storage{Barrier: 5}, "every write would fail"},
		{"negative replicas", fleet.Storage{Replicas: -1}, "places nothing"},
		{"negative barrier", fleet.Storage{Barrier: -1}, "promises nothing"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.in.Resolved(3, 2)
			require.ErrorIs(t, err, fleet.ErrMalformed)
			require.ErrorContains(t, err, c.says)
		})
	}
}

// TestDECODECHECKSSHAPEANDNOTTHEPAIR. A negative is wrong at every reader's
// defaults, so it is refused here; the pair is not, so it is not.
func TestAMalformedStorageBlockIsRefusedAtDecode(t *testing.T) {
	for _, bad := range [][]byte{
		[]byte(`{"storage":{"replicas":-1}}`),
		[]byte(`{"storage":{"barrier":-1}}`),
	} {
		_, err := fleet.Decode(bad)
		require.ErrorIs(t, err, fleet.ErrMalformed, "%q", bad)
	}

	// And the pair Decode cannot judge passes it, because a zero there is the
	// reader's number and not this package's.
	back, err := fleet.Decode([]byte(`{"storage":{"barrier":5}}`))
	require.NoError(t, err)
	require.Equal(t, 5, back.Storage.Barrier)
}

// TestRetentionIsWrittenAsAnOperatorWritesIt: T round-trips as a duration
// string, a cloud that says nothing says zero, and a negative or unreadable
// one is refused like any corrupt document.
func TestRetentionIsWrittenAsAnOperatorWritesIt(t *testing.T) {
	raw, err := fleet.Encode(fleet.Fleet{Retention: fleet.Duration(720 * time.Hour)})
	require.NoError(t, err)
	require.JSONEq(t, `{"retention":"720h0m0s"}`, string(raw))

	back, err := fleet.Decode([]byte(`{"retention":"36h"}`))
	require.NoError(t, err)
	require.Equal(t, 36*time.Hour, time.Duration(back.Retention))

	none, err := fleet.Encode(fleet.Fleet{Replication: 1})
	require.NoError(t, err)
	require.NotContains(t, string(none), "retention")

	for _, bad := range []string{`{"retention":"-1h"}`, `{"retention":"soon"}`, `{"retention":3600}`} {
		_, err := fleet.Decode([]byte(bad))
		require.ErrorIs(t, err, fleet.ErrMalformed, bad)
	}
}
