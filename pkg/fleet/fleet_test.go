package fleet_test

import (
	"testing"

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
