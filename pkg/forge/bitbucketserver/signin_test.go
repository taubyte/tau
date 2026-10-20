package bitbucketserver

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
)

// The sign-in endpoints are on the driver's own host, so a sign-in cannot be
// aimed at another server.
func TestOAuthEndpointsAreTheHosts(t *testing.T) {
	d, err := New("git.example.com")
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: "git.example.com", Driver: d})
	require.NoError(t, err)
	o, err := reg.OAuth("git.example.com")
	require.NoError(t, err)
	require.Equal(t, "https://git.example.com/rest/oauth2/latest/authorize", o.AuthorizeURL)
	require.Equal(t, "https://git.example.com/rest/oauth2/latest/token", o.TokenURL)
	require.Equal(t, "", o.DeviceURL)
}
