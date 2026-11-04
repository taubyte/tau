package ingress

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestATagPushIsIgnoredByPolicy(t *testing.T) {
	h := newHarness(t, Config{})
	require.False(t, h.eligible("refs/tags/v1.0.0"))
}

func TestEligibilityIsExactAndNotAPrefix(t *testing.T) {
	h := newHarness(t, Config{EligibleRefs: []string{"refs/heads/main"}})

	require.True(t, h.eligible("refs/heads/main"))
	require.False(t, h.eligible("refs/heads/mainline"))
	require.False(t, h.eligible("refs/heads/main/x"))
	require.False(t, h.eligible("Refs/Heads/Main"))
	require.False(t, h.eligible(""))
}
