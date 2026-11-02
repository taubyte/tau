package node

import (
	"testing"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node/cache"
	"github.com/taubyte/tau/pkg/invoke"
	invokesecret "github.com/taubyte/tau/pkg/invoke/secret"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// Only the wiring is tested here: one named database, opened on first need, closed
// with the node.

// grant is one cache entry as services/worker builds it.
func grant(handle string, settings map[string]string) invoke.Grant {
	return invoke.Grant{
		Kind: "cache", Label: "sessions", Handle: handle,
		Ops: []string{"get", "put", "delete", "list"}, Settings: settings,
	}
}

func backing(t *testing.T) (wasmtier.Caches, *memFactory) {
	t.Helper()

	n, _, factory := makeNode(t, Config{Services: []string{Worker, Discovery}})
	c, err := cache.New(cacheStores{node: n})
	require.NoError(t, err)
	return c, factory
}

func TestEveryCacheSharesOneDatabase(t *testing.T) {
	c, factory := backing(t)
	ctx := t.Context()

	for _, handle := range []string{"shop/prod/QmA", "shop/prod/QmB", "other/prod/QmA"} {
		_, err := c.Open(ctx, grant(handle, nil))
		require.NoError(t, err)
	}

	opened, _ := factory.counts()
	var caches int
	for _, name := range opened {
		if name == cacheDatabase {
			caches++
		}
	}
	require.Equal(t, 1, caches, "three caches, one database: %v", opened)
}

func TestACacheNeedsADatabaseFactory(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	n, err := New(Config{Services: []string{Worker, Discovery}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithRuntimes(&tier{name: "wasm"}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	c, err := cache.New(cacheStores{node: n})
	require.NoError(t, err)

	_, err = c.Open(t.Context(), grant("shop/prod/QmA", nil))
	require.ErrorIs(t, err, ErrNoCacheStore)
}

func TestTheCacheDatabaseIsClosedWithTheNode(t *testing.T) {
	n, _, factory := makeNode(t, Config{Services: []string{Worker, Discovery}})

	c, err := cache.New(cacheStores{node: n})
	require.NoError(t, err)
	_, err = c.Open(t.Context(), grant("shop/prod/QmA", nil))
	require.NoError(t, err)

	require.NoError(t, n.Close())
	_, closed := factory.counts()
	require.Positive(t, closed)
}

func TestOneVaultServesTheWorkerAndTheTier(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery, Secrets}})
	require.NoError(t, n.Start(t.Context()))
	ctx := t.Context()

	require.NoError(t, n.Vault().Set(ctx, "shop", "prod", "QmStripe", []byte("sk_live")))

	grant := invoke.Grant{
		Kind: "secret", Label: "stripe", Handle: "shop/prod/QmStripe", Ops: []string{"get"},
	}

	value, err := n.vault.SecretFor(ctx, grant)
	require.NoError(t, err)
	require.Equal(t, "sk_live", string(value))

	// The worker's own path reads the same row.
	same, err := n.vault.Secret(ctx, "shop", "prod", "QmStripe")
	require.NoError(t, err)
	require.Equal(t, "sk_live", same)

	// AND A ROTATION IS SEEN, which is the whole reason the tier reads per call.
	require.NoError(t, n.Vault().Set(ctx, "shop", "prod", "QmStripe", []byte("rotated")))
	value, err = n.vault.SecretFor(ctx, grant)
	require.NoError(t, err)
	require.Equal(t, "rotated", string(value))
}

func TestAnUnsetSecretIsTheTiersArm(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery, Secrets}})
	require.NoError(t, n.Start(t.Context()))

	_, err := n.vault.SecretFor(t.Context(), invoke.Grant{
		Kind: "secret", Label: "stripe", Handle: "shop/prod/QmNeverSet", Ops: []string{"get"},
	})
	require.ErrorIs(t, err, invokesecret.ErrNotSet)
}

func TestAMalformedSecretHandleIsRefused(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery, Secrets}})
	require.NoError(t, n.Start(t.Context()))

	for _, handle := range []string{"", "shop", "shop/prod", "shop//QmStripe"} {
		_, err := n.vault.SecretFor(t.Context(), invoke.Grant{
			Kind: "secret", Label: "stripe", Handle: handle, Ops: []string{"get"},
		})
		require.ErrorContains(t, err, "does not address a secret", "handle %q", handle)
	}
}
