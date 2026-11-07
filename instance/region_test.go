package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

func TestACarriedCallOnANodePlacingNothingIsRefusedByName(t *testing.T) {
	_, err := regionOrigins{n: &Instance{}}.Invoke(t.Context(), specs.Address{}, nil)
	require.ErrorIs(t, err, ErrNoPlacement)
	require.ErrorContains(t, WithRegionLink(nil)(&options{}), "nil region link")
}
