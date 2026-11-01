package register_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/storekey"
)

func TestAHostNameCannotAddressAnotherProjectsRow(t *testing.T) {
	reg, err := register.New(register.WithStore(register.NewMemStore()))
	require.NoError(t, err)

	_, _, err = reg.ResolveHost(context.Background(), "../p/victim/prod")
	require.ErrorIs(t, err, storekey.ErrNotCanonical,
		"a Host header must not address a row of another kind")
}

func TestAPublishCannotClaimANameThatIsAnotherRow(t *testing.T) {
	reg, err := register.New(register.WithStore(register.NewMemStore()))
	require.NoError(t, err)

	_, err = reg.Publish(context.Background(), register.Publication{
		Project: "attacker", Environment: "prod", Revision: 1,
		Compiled: []byte(`{"object":{"id":"x","name":"attacker"}}`),
		Hosts:    []string{"../p/victim/prod"},
	})
	require.ErrorIs(t, err, storekey.ErrNotCanonical,
		"a declared host must not write over another project's pointer")
}
