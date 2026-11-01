package gitea

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
)

func TestOAuthEndpointsAreTheHosts(t *testing.T) {
	d, err := New("codeberg.org")
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: "codeberg.org", Driver: d})
	require.NoError(t, err)
	o, err := reg.OAuth("codeberg.org")
	require.NoError(t, err)
	require.Equal(t, "https://codeberg.org/login/oauth/authorize", o.AuthorizeURL)
	require.Equal(t, "https://codeberg.org/login/oauth/access_token", o.TokenURL)
	require.Equal(t, "", o.DeviceURL)
}
