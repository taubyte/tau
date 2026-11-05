package dream

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

func TestAUniverseWithNoComputeRefusesByName(t *testing.T) {
	require.Equal(t, "none", absent{}.Name())
	_, err := absent{}.Start(t.Context(), invoke.Spec{})
	require.ErrorIs(t, err, invoke.ErrNoRuntime)
}
