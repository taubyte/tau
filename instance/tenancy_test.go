package instance

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/services"
)

func TestTheTenancyIsCanonicalAndCheckedAtConstruction(t *testing.T) {
	c, err := Config{Services: []string{Stats}, Tenancy: []string{"GitHub.com/Acme", "github.com/acme"}}.withDefaults(services.Names()...)
	require.NoError(t, err)
	require.Equal(t, []string{"github.com/acme"}, c.Tenancy)

	_, err = Config{Services: []string{Stats}, Tenancy: []string{"acme"}}.withDefaults(services.Names()...)
	require.ErrorContains(t, err, "not a namespace")
}
