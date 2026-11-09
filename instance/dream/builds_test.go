package dream

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPauseEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, pause(ctx), context.Canceled)
	require.NoError(t, pause(t.Context()))
}
