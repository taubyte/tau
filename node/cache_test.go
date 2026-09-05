package node

import (
	"errors"
	"testing"
	"time"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/cache"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// grant is one cache entry as services/worker builds it.
func grant(handle string, settings map[string]string) invoke.Grant {
	return invoke.Grant{
		Kind: "cache", Label: "sessions", Handle: handle,
		Ops: []string{"get", "put", "delete", "list"}, Settings: settings,
	}
}

func backing(t *testing.T) (*caches, *memFactory) {
	t.Helper()
	n, _, factory := makeNode(t, Config{Services: []string{Worker, Stats}})
	return &caches{node: n}, factory
}

// TestOneDatabaseHoldsEveryCache. A kvdb per cache would be a CRDT, a
// broadcaster and a pubsub topic per resource; one database with the handle as
// the prefix is the same isolation with one set of moving parts.
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

// TestAnOpenedCacheIsReused, because pkg/cache scans what this node holds when
// it opens and paying that per invocation would put it on the request path.
func TestOpeningTheSameHandleTwiceIsTheSameCache(t *testing.T) {
	c, _ := backing(t)
	ctx := t.Context()

	first, err := c.Open(ctx, grant("shop/prod/QmA", nil))
	require.NoError(t, err)
	again, err := c.Open(ctx, grant("shop/prod/QmA", nil))
	require.NoError(t, err)

	// The wrapper is a value, so the identity that matters is the cache under
	// it — one open, one seed.
	require.Same(t, first.(translated).Cache, again.(translated).Cache)
	require.Len(t, c.opened, 1)
}

// TestTwoHandlesAreTwoCaches, which is what the prefix buys — and the check
// that matters is that one cannot read the other.
func TestTwoHandlesDoNotSeeEachOther(t *testing.T) {
	c, _ := backing(t)
	ctx := t.Context()

	mine, err := c.Open(ctx, grant("shop/prod/QmA", nil))
	require.NoError(t, err)
	theirs, err := c.Open(ctx, grant("shop/prod/QmB", nil))
	require.NoError(t, err)

	require.NoError(t, mine.Put(ctx, "k", []byte("mine")))
	require.NoError(t, theirs.Put(ctx, "k", []byte("theirs")))

	value, err := mine.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "mine", string(value))

	names, err := theirs.List(ctx, "")
	require.NoError(t, err)
	require.Equal(t, []string{"k"}, names)
}

// TestAnEnvironmentIsItsOwnData. One declaration, two deployments: staging's
// entries and production's are the same cache in the config and different rows
// on disk.
func TestTwoDeploymentsOfOneCacheAreSeparate(t *testing.T) {
	c, _ := backing(t)
	ctx := t.Context()

	prod, err := c.Open(ctx, grant("shop/prod/QmA", nil))
	require.NoError(t, err)
	staging, err := c.Open(ctx, grant("shop/staging/QmA", nil))
	require.NoError(t, err)

	require.NoError(t, prod.Put(ctx, "k", []byte("live")))
	_, err = staging.Get(ctx, "k")
	require.Error(t, err, "staging cannot read production's")
}

// ── what the target declared ─────────────────────────────────────────────────

// TestTheDeclaredCeilingIsEnforced, from the settings the grant carried rather
// than from a lookup this backing would have to make on the request path.
func TestTheDeclaredSizeIsEnforced(t *testing.T) {
	c, _ := backing(t)
	ctx := t.Context()

	cache, err := c.Open(ctx, grant("shop/prod/QmA", map[string]string{cacheSize: "20"}))
	require.NoError(t, err)

	require.NoError(t, cache.Put(ctx, "a", []byte("012345678")))
	require.NoError(t, cache.Put(ctx, "b", []byte("012345678")))
	require.NoError(t, cache.Put(ctx, "c", []byte("012345678")), "it evicted rather than refusing")

	names, err := cache.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, names, 2, "the ceiling held")
}

func TestTheDeclaredLifetimeIsRead(t *testing.T) {
	c, _ := backing(t)

	cache, err := c.Open(t.Context(), grant("shop/prod/QmA", map[string]string{cacheTTL: "24h0m0s"}))
	require.NoError(t, err)
	require.NotNil(t, cache)

	require.Equal(t, 24*time.Hour, duration("24h0m0s"))
}

// TestAnUnreadableSettingIsNoLimitRatherThanARefusal. A cache with no ceiling
// is a bug to find in a log; one that refuses every write is an outage.
func TestAnUnparseableSettingIsNoLimit(t *testing.T) {
	require.Zero(t, duration(""))
	require.Zero(t, duration("not a duration"))
	require.Zero(t, byteCount(""))
	require.Zero(t, byteCount("32MB"), "the composer writes bytes; a human string is not one")
	require.Equal(t, int64(32000000), byteCount("32000000"))
}

// TestANodeWithNoDatabaseFactoryCannotOpenACache, and says so rather than
// panicking on the first request that reaches one.
func TestACacheNeedsADatabaseFactory(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	n, err := New(Config{Services: []string{Worker, Stats}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithRuntimes(&tier{name: "wasm"}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	_, err = (&caches{node: n}).Open(t.Context(), grant("shop/prod/QmA", nil))
	require.ErrorIs(t, err, ErrNoCacheStore)
}

// TestTheCacheDatabaseIsClosedWithTheNode, so a node that opened one on its
// first request does not leak it.
func TestTheCacheDatabaseIsClosedWithTheNode(t *testing.T) {
	n, _, factory := makeNode(t, Config{Services: []string{Worker, Stats}})

	_, err := (&caches{node: n}).Open(t.Context(), grant("shop/prod/QmA", nil))
	require.NoError(t, err)

	require.NoError(t, n.Close())
	_, closed := factory.counts()
	require.Positive(t, closed)
}

// ── the sentinels the tier acts on ───────────────────────────────────────────

// TestAMissArrivesAsTheTIERSMiss, WHICH THREE PASSING UNIT TESTS DID NOT CATCH.
//
// pkg/cache has its own ErrNotFound and pkg/invoke/wasm has another; the tier
// maps anything it does not recognise onto the guest's `unavailable` arm. So a
// cache miss — the common case, the thing a cache is FOR — was reported to
// every guest as an outage, and an author would have written a retry around it.
// Each package was right on its own and the composition was wrong, which is why
// the dream test that runs a real component through a real store exists.
func TestPkgCacheSentinelsBecomeTierSentinels(t *testing.T) {
	c, _ := backing(t)
	ctx := t.Context()

	held, err := c.Open(ctx, grant("shop/prod/QmA", map[string]string{cacheSize: "10"}))
	require.NoError(t, err)

	_, err = held.Get(ctx, "never-written")
	require.ErrorIs(t, err, wasmtier.ErrNotFound, "a miss is the tier's miss")
	require.ErrorIs(t, err, cache.ErrNotFound, "and the cause is still readable in a log")

	err = held.Put(ctx, "k", make([]byte, 100))
	require.ErrorIs(t, err, wasmtier.ErrTooLarge)

	// And the SECOND open of the same handle answers the same way — the cached
	// path returned the raw cache once, which is how this was found.
	again, err := c.Open(ctx, grant("shop/prod/QmA", nil))
	require.NoError(t, err)
	_, err = again.Get(ctx, "never-written")
	require.ErrorIs(t, err, wasmtier.ErrNotFound, "the reused cache is wrapped too")
}

// TestAnythingElseIsLeftAlone, so a store that could not answer stays
// distinguishable from one that had nothing to say.
func TestAnUnrecognisedFailureIsNotTranslated(t *testing.T) {
	other := errors.New("the swarm is unreachable")
	require.Equal(t, other, asTierError(other))
	require.NoError(t, asTierError(nil))
}
