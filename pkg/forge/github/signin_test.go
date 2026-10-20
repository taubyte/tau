package github

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
)

// The sign-in endpoints are on the driver's own host, so a sign-in cannot be
// aimed at another server.
func TestOAuthEndpointsAreTheHosts(t *testing.T) {
	d, err := New("github.com")
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: "github.com", Driver: d})
	require.NoError(t, err)
	o, err := reg.OAuth("github.com")
	require.NoError(t, err)
	require.Equal(t, "https://github.com/login/oauth/authorize", o.AuthorizeURL)
	require.Equal(t, "https://github.com/login/oauth/access_token", o.TokenURL)
	require.Equal(t, "https://github.com/login/device/code", o.DeviceURL)
}
