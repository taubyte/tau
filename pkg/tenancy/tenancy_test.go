package tenancy

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

func key(t *testing.T, url string) repokey.Key {
	t.Helper()
	k, err := repokey.Parse(url)
	require.NoError(t, err)
	return k
}

func TestNamespacesAreCanonical(t *testing.T) {
	ten, err := Parse([]string{"GitHub.com/Acme", " github.com/acme/ ", "gitlab.example.com:443/Team", "git.internal:8443/ops"})
	require.NoError(t, err)
	require.True(t, ten.Configured())
	require.Equal(t, []string{"git.internal:8443/ops", "github.com/acme", "gitlab.example.com/team"}, ten.Namespaces())
}

func TestWhatIsNotANamespaceIsRefused(t *testing.T) {
	for _, entry := range []string{"", "acme", "github.com", "github.com/acme/pay", "github.com/acme/sub/group", "https://github.com/acme"} {
		_, err := Parse([]string{entry})
		require.Error(t, err, entry)
	}
}

func TestNoTenancyServesEverything(t *testing.T) {
	var ten Tenancy
	require.False(t, ten.Configured())
	require.Nil(t, ten.Namespaces())
	require.NoError(t, ten.Refuse(key(t, "https://github.com/anyone/repo")))
}

func TestAServedNamespaceAndAnotherOne(t *testing.T) {
	ten, err := Parse([]string{"github.com/acme"})
	require.NoError(t, err)

	require.True(t, ten.Serves(key(t, "git@github.com:Acme/Pay.git")))
	require.True(t, ten.Serves(key(t, "https://github.com/acme/sub/deep")), "a subgroup belongs to its group")
	require.False(t, ten.Serves(key(t, "https://gitlab.com/acme/pay")), "the namespace is on its host")

	err = ten.Refuse(key(t, "https://github.com/other/pay"))
	require.EqualError(t, err, "github.com/other/pay is outside this cloud's tenancy; it serves github.com/acme")
}
