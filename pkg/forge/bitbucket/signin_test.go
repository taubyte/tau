package bitbucket

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
)

// The sign-in endpoints are on the driver's own host, so a sign-in cannot be
// aimed at another server.
func TestOAuthEndpointsAreTheHosts(t *testing.T) {
	d, err := New("bitbucket.org")
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: "bitbucket.org", Driver: d})
	require.NoError(t, err)
	o, err := reg.OAuth("bitbucket.org")
	require.NoError(t, err)
	require.Equal(t, "https://bitbucket.org/site/oauth2/authorize", o.AuthorizeURL)
	require.Equal(t, "https://bitbucket.org/site/oauth2/access_token", o.TokenURL)
	require.Equal(t, "", o.DeviceURL)
}
