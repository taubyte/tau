package instance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	secretsService "github.com/taubyte/tau/services/secrets"
)

// The vault's write half, the methods `tau secret set` reaches; fleet_test covers
// reads.

func TestTheDevelopersHalfWritesWhereTheRequestPathReads(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats, Secrets}})
	require.NoError(t, n.Start(t.Context()))
	v := n.Vault()
	require.NoError(t, v.Set(t.Context(), "shop", "prod", secretID, []byte("sk_live")))

	names, err := v.List(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, []string{secretID}, names)

	value, err := v.Secret(t.Context(), "shop", "prod", secretID)
	require.NoError(t, err)
	require.Equal(t, "sk_live", value)

	// AN ENVIRONMENT IS PART OF THE ADDRESS. Staging's secrets are not
	// production's, and a list that crossed them would hand a developer the
	// wrong name to reference.
	staging, err := v.List(t.Context(), "shop", "staging")
	require.NoError(t, err)
	require.Empty(t, staging)

	require.NoError(t, v.Delete(t.Context(), "shop", "prod", secretID))
	names, err = v.List(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Empty(t, names)
}

func TestTheWriteHalfGoesToWhicheverNodeHoldsTheVault(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Secrets, Stats}},
		Config{Services: []string{Worker, Stats}},
	)
	holder, frontend := nodes[0], nodes[1]

	finds(t, frontend, secretsService.Protocol)

	require.NoError(t, frontend.Vault().Set(t.Context(), "shop", "prod", secretID, []byte("sk_live")))

	// READ IT BACK ON THE NODE THAT HOLDS IT, not through the same stream that
	// wrote it — that is what makes this a write to the vault rather than to a
	// cache in front of one.
	value, err := holder.Vault().Secret(t.Context(), "shop", "prod", secretID)
	require.NoError(t, err)
	require.Equal(t, "sk_live", value)

	names, err := frontend.Vault().List(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, []string{secretID}, names)

	require.NoError(t, frontend.Vault().Delete(t.Context(), "shop", "prod", secretID))
	names, err = holder.Vault().List(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Empty(t, names)
}

func TestAWriteWithNoVaultAnywhereIsRefused(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register}})
	require.NoError(t, n.Start(t.Context()))

	// This node runs no vault and finds no node running one, so the
	// vault has nothing to open — which is the shape a `Services` list that
	// forgot the secrets service produces.
	v := n.Vault()

	require.ErrorIs(t, v.Set(t.Context(), "shop", "prod", secretID, []byte("x")), secretsService.ErrNoSecrets)
	require.ErrorIs(t, v.Delete(t.Context(), "shop", "prod", secretID), secretsService.ErrNoSecrets)

	_, err := v.List(t.Context(), "shop", "prod")
	require.ErrorIs(t, err, secretsService.ErrNoSecrets)

	_, err = v.Secret(t.Context(), "shop", "prod", secretID)
	require.ErrorIs(t, err, secretsService.ErrNoSecrets)
}

func TestInitWritesTheCloudsSecretsTheNodeThenServes(t *testing.T) {
	ctx := t.Context()
	p := mockPeer(t)
	root := t.TempDir()

	ref := secretsService.Ref{Scope: secretsService.Cloud("auth"), Name: "pepper"}
	sealed, err := secretsService.Sealed(ref, []byte("salt"), time.Now())
	require.NoError(t, err)
	bundle := &secretsv2.InitBundle{Secrets: []*secretsv2.InitSecret{sealed}}

	require.ErrorIs(t, secretsService.Init(ctx, p, "", secretsService.Config{}, bundle), secretsService.ErrNoRoot)
	require.NoError(t, secretsService.Init(ctx, p, root, secretsService.Config{}, bundle))
	cfg := Config{Services: []string{Register, Stats, Secrets}}

	// THE SAME PEER, so the node opens the database init wrote into.
	n, err := New(p, rootedIn(t, cfg, root), withRaft(mockGroups{}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NoError(t, n.Start(ctx))

	cloud := n.Vault().(*secretsService.Vault).Cloud("auth")
	got, err := cloud.Get(ctx, "pepper")
	require.NoError(t, err)
	require.Equal(t, "salt", string(got))

	require.NoError(t, cloud.Set(ctx, "signing", []byte("key")))
	names, err := cloud.List(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"pepper", "signing"}, names)

	_, err = n.Vault().Secret(ctx, "cloud", "auth", "pepper")
	require.ErrorIs(t, err, secretsService.ErrNotSet, "a project named cloud reaches no cloud secret")
}
