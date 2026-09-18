package worker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTheDECLAREDPortsReachTheTier. The vocabulary has carried `ports:` since
// v2 began and nothing read them — so a container binding a fixed port was
// given an ephemeral one in $PORT and mapped to that same ephemeral number,
// which reached nothing it was listening on.
func TestAContainersDeclaredPortsReachTheSpec(t *testing.T) {
	h := newHarness(t)

	spec, err := h.Worker.Resolve(t.Context(), Target{
		Project: "shop", Environment: "prod", Kind: "application", Name: "db",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"pg": 5432}, spec.Ports,
		"postgres binds 5432 because that is what postgres does")
}

// TestAWorkloadThatDeclaresNoneCarriesNone, because an empty map and a nil one
// mean different things to the tier: nil is the $PORT contract, which is still
// right for something built for this platform.
func TestAWorkloadWithNoPortsCarriesNil(t *testing.T) {
	h := newHarness(t)

	spec, err := h.Worker.Resolve(t.Context(), Target{
		Project: "shop", Environment: "prod", Kind: "function", Name: "checkout",
	})
	require.NoError(t, err)
	require.Nil(t, spec.Ports)
}
