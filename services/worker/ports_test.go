package worker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAContainersDeclaredPortsReachTheSpec(t *testing.T) {
	h := newHarness(t)

	spec, err := h.Worker.Resolve(t.Context(), Target{
		Project: "shop", Environment: "prod", Kind: "application", Name: "db",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"pg": 5432}, spec.Ports,
		"postgres binds 5432 because that is what postgres does")
}

func TestAWorkloadWithNoPortsCarriesNil(t *testing.T) {
	h := newHarness(t)

	spec, err := h.Worker.Resolve(t.Context(), Target{
		Project: "shop", Environment: "prod", Kind: "function", Name: "checkout",
	})
	require.NoError(t, err)
	require.Nil(t, spec.Ports)
}
