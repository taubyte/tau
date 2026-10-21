package forgeauth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForgesNeedARegistry(t *testing.T) {
	_, err := New(nil)
	require.Error(t, err)
}
