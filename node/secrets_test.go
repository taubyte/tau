package node

import (
	"context"
	"errors"
	"testing"
	"time"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/raft"
	secretsService "github.com/taubyte/tau/services/secrets"
)

// secrets_test covers the WRITE half of the vault.
//
// The read half is covered by fleet_test, which resolves a workload's env from
// a vault here and from one on another node. Nothing exercised the other three
// methods, and they are the ones a developer's `tau secret set` reaches — the
// only write in v2 that is not a git push.

// TestTheDevelopersHalfWritesWhereTheRequestPathReads. Set/List/Delete and
// Secret are one store seen from two sides; a write that landed somewhere the
// read does not look is a secret that exists and cannot be used.
func TestTheDevelopersHalfWritesWhereTheRequestPathReads(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery, Secrets}})
	require.NoError(t, n.Start(t.Context()))
	require.NotNil(t, n.vault, "a node that runs a worker builds a vault")

	v := n.vault
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

// TestTheWriteHalfGoesToWhicheverNodeHoldsTheVault. `open` is one rule for all
// four methods — run it here, use it directly; otherwise ask discovery who does —
// so a build frontend on a node with no vault writes over a stream to one that
// has it, and the holder reads back what it was sent.
func TestTheWriteHalfGoesToWhicheverNodeHoldsTheVault(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Secrets, Discovery}},
		Config{Services: []string{Worker, Discovery}},
	)
	holder, frontend := nodes[0], nodes[1]

	sees(t, frontend, Secrets)
	require.NotNil(t, frontend.vault)

	require.NoError(t, frontend.vault.Set(t.Context(), "shop", "prod", secretID, []byte("sk_live")))

	// READ IT BACK ON THE NODE THAT HOLDS IT, not through the same stream that
	// wrote it — that is what makes this a write to the vault rather than to a
	// cache in front of one.
	value, err := holder.Vault().Secret(t.Context(), "shop", "prod", secretID)
	require.NoError(t, err)
	require.Equal(t, "sk_live", value)

	names, err := frontend.vault.List(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, []string{secretID}, names)

	require.NoError(t, frontend.vault.Delete(t.Context(), "shop", "prod", secretID))
	names, err = holder.Vault().List(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Empty(t, names)
}

// TestAWriteWithNoVaultAnywhereIsRefused. The write half refuses for the same
// reason the read half does: a `tau secret set` that reported success against
// no vault is a developer who believes the secret is there.
func TestAWriteWithNoVaultAnywhereIsRefused(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register}})
	require.NoError(t, n.Start(t.Context()))

	// This node runs neither a vault nor the discovery that would find one, so the
	// vault has nothing to open — which is the shape a `Services` list that
	// forgot the secrets service produces.
	v := newVault(n)
	t.Cleanup(func() { v.Close() })

	require.ErrorIs(t, v.Set(t.Context(), "shop", "prod", secretID, []byte("x")), ErrNoSecrets)
	require.ErrorIs(t, v.Delete(t.Context(), "shop", "prod", secretID), ErrNoSecrets)

	_, err := v.List(t.Context(), "shop", "prod")
	require.ErrorIs(t, err, ErrNoSecrets)

	_, err = v.Secret(t.Context(), "shop", "prod", secretID)
	require.ErrorIs(t, err, ErrNoSecrets)
}

// secretFor is an injected resolver, which is the one thing that outranks a
// node's own vault.
type secretFor func(ctx context.Context, project, environment, id string) (string, error)

func (f secretFor) Secret(ctx context.Context, project, environment, id string) (string, error) {
	return f(ctx, project, environment, id)
}

// TestAnInjectedResolverOutranksTheNodesOwnVault. Three consumers ask this
// question, which is why it is a method rather than three copies of the switch
// — and a copy that disagreed would give one of them a different secret.
func TestAnInjectedResolverOutranksTheNodesOwnVault(t *testing.T) {
	injected := secretFor(func(context.Context, string, string, string) (string, error) {
		return "from the injected one", nil
	})

	with, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery, Secrets}}, WithSecrets(injected))
	require.NoError(t, with.Start(t.Context()))
	got, err := with.secretResolver().Secret(t.Context(), "shop", "prod", secretID)
	require.NoError(t, err)
	require.Equal(t, "from the injected one", got)

	// WITHOUT ONE IT IS THIS NODE'S VAULT...
	own, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery, Secrets}})
	require.NoError(t, own.Start(t.Context()))
	require.Equal(t, own.vault, own.secretResolver())

	// ...AND WITH NEITHER IT IS NOTHING, which is what makes a resource naming
	// a secret refused rather than started without it.
	bare, _, _ := makeNode(t, Config{Services: []string{Register}})
	require.NoError(t, bare.Start(t.Context()))
	require.Nil(t, bare.secretResolver())
}

// TestInitWritesTheCloudsSecretsTheNodeThenServes: what `taud init` writes on
// a stopped node is in its vault once it starts, in the cloud scope its own
// services read and no project can name.
func TestInitWritesTheCloudsSecretsTheNodeThenServes(t *testing.T) {
	ctx := t.Context()
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)
	factory := &memFactory{}
	root := t.TempDir()

	ref := secretsService.Ref{Scope: secretsService.Cloud("auth"), Name: "pepper"}
	sealed, err := secretsService.Sealed(ref, []byte("salt"), time.Now())
	require.NoError(t, err)
	bundle := &secretsv2.InitBundle{Secrets: []*secretsv2.InitSecret{sealed}}

	require.ErrorIs(t, InitSecrets(ctx, Config{Services: []string{Register}}, root, h, factory, bundle), ErrNoSecretsHere)
	cfg := Config{Services: []string{Register, Discovery, Secrets}}
	failing := &memFactory{err: errors.New("no database")}
	require.ErrorContains(t, InitSecrets(ctx, cfg, root, h, failing, bundle), "no database")
	require.ErrorIs(t, InitSecrets(ctx, cfg, "", h, factory, bundle), secretsService.ErrNoRoot)
	require.ErrorIs(t, InitSecrets(ctx, Config{}, "", h, factory, bundle), secretsService.ErrNoRoot, "no services named runs secrets")
	require.NoError(t, InitSecrets(ctx, cfg, root, h, factory, bundle))

	n, err := New(cfg,
		WithHost(h),
		WithPubSub(newPubSub(t, h)),
		WithDatabases(factory),
		WithRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }),
		WithRoot(root),
	)
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NoError(t, n.Start(ctx))

	cloud := n.Vault().(*vault).cloud("auth")
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
