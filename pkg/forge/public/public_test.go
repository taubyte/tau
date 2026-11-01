package public

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
)

func TestThePublicForgesAreBound(t *testing.T) {
	r, err := Registry(forge.Policy{})
	require.NoError(t, err)
	require.Equal(t, []string{"github.com", "gitlab.com"}, r.Hosts())
}

func TestEachPublicForgeSignsUnderItsOwnScheme(t *testing.T) {
	r, err := Registry(forge.Policy{})
	require.NoError(t, err)
	for host, want := range map[string]forge.Scheme{
		"github.com": forge.SchemeHubSHA256,
		"gitlab.com": forge.SchemeStandardWebhooks,
	} {
		d, err := r.Driver(host)
		require.NoError(t, err)
		s, ok := d.(forge.Signatures)
		require.True(t, ok, "%s verifies no deliveries at all", host)
		require.Equal(t, want, s.Scheme(), host)
	}
}
