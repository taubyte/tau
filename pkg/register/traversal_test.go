package register_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/storekey"
)

// TestAHostNameCannotAddressAnotherProjectsRow.
//
// A REGISTER KEY IS A FIXED PREFIX PLUS SOMEBODY ELSE'S TEXT: "h/"+fqdn for who
// answers on a name, "p/"+project+"/"+environment for what they are serving.
// The fqdn on the read path is the HTTP Host header, which is as untrusted as
// input gets, and the stores put the key through ds.NewKey — path.Clean — so a
// name holding ".." reached a different row entirely:
//
//	"h/" + "../p/victim/prod"  ->  /p/victim/prod
//
// On a resolve that reads another project's pointer. On a publish, whose hosts
// come from the project's own declaration, it WRITES one.
func TestAHostNameCannotAddressAnotherProjectsRow(t *testing.T) {
	reg, err := register.New(register.WithStore(register.NewMemStore()))
	require.NoError(t, err)

	_, _, err = reg.ResolveHost(context.Background(), "../p/victim/prod")
	require.ErrorIs(t, err, storekey.ErrNotCanonical,
		"a Host header must not address a row of another kind")
}

// TestAPublishCannotClaimANameThatIsAnotherRow — the write direction, which is
// the one that takes a deployment over rather than merely reading it.
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
