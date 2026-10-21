package linkcreds

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestADeployKeyIsNamedPerRepositoryHoweverItIsSpelled(t *testing.T) {
	a := DeployKey("github.com/Acme/Shop")
	require.Equal(t, a, DeployKey(" github.com/acme/shop/ "))
	require.NotEqual(t, a, DeployKey("github.com/acme/shop-code"))
	require.Len(t, a, len("deploy-key-")+64)
	require.LessOrEqual(t, len(a), 128, "a secret name is at most 128 bytes")
}

func TestAHookSecretIsNamedPerRepositoryApartFromItsDeployKey(t *testing.T) {
	a := HookSecret("gitlab.com/Grp/Sub/Svc")
	require.Equal(t, a, HookSecret("gitlab.com/grp/sub/svc/"))
	require.NotEqual(t, a, HookSecret("gitlab.com/grp/sub/other"))
	require.NotEqual(t, a, DeployKey("gitlab.com/grp/sub/svc"))
	require.Len(t, a, len("hook-secret-")+64)
}
