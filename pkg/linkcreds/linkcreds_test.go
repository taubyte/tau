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
