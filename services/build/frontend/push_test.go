package frontend

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Reading a push out of a delivery is the forge's (pkg/forge, per driver). What
// is left here is the one policy this role owns: which refs are built.

// A tag push carries refs/tags/…, which is not a branch and is not in any
// eligible list. It is refused by the ref policy rather than by a second check.
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
