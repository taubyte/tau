package cache_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	pkgcache "github.com/taubyte/tau/pkg/cache"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/services/worker/cache"
)

// The adapter over a map, with real pkg/cache caches, so the translation is not
// asserted against itself.

// rows is a Store double: a map, and a record of what was asked of it.
type rows struct {
	mu     sync.Mutex
	held   map[string][]byte
	listed []string

	// getErr, putErr and listErr are a store that cannot answer, which is the
	// arm that must stay distinguishable from a store that had nothing to say.
	getErr, putErr, listErr error
}

func newRows() *rows { return &rows{held: map[string][]byte{}} }

func (r *rows) Get(_ context.Context, key string) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getErr != nil {
		return nil, false, r.getErr
	}
	v, ok := r.held[key]
	return v, ok, nil
}

func (r *rows) Put(_ context.Context, key string, value []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.putErr != nil {
		return r.putErr
	}
	r.held[key] = value
	return nil
}

func (r *rows) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.held, key)
	return nil
}

// List answers WHOLE KEYS, which is the shape pkg/cache's seed cuts its prefix
// out of.
func (r *rows) List(_ context.Context, prefix string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listErr != nil {
		return nil, r.listErr
	}
	r.listed = append(r.listed, prefix)

	var out []string
	for key := range r.held {
		if strings.HasPrefix(key, prefix) {
			out = append(out, key)
		}
	}
	return out, nil
}

func (r *rows) seeds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.listed...)
}

// opener is a Stores double: one store, and a count of how often it was asked
// for.
type opener struct {
	store *rows
	asked int
	err   error
}

func (o *opener) Store() (pkgcache.Store, error) {
	o.asked++
	if o.err != nil {
		return nil, o.err
	}
	return o.store, nil
}

// caching is the adapter over a fresh map.
func caching(t *testing.T) (*cache.Caches, *opener) {
	t.Helper()

	held := &opener{store: newRows()}
	c, err := cache.New(held)
	require.NoError(t, err)
	return c, held
}

// grant is one cache entry as services/worker builds it.
func grant(handle string, settings map[string]string) invoke.Grant {
	return invoke.Grant{
		Kind: "cache", Label: "sessions", Handle: handle,
		Ops: []string{"get", "put", "delete", "list"}, Settings: settings,
	}
}

func TestTheAdapterNeedsSomewhereToPutRows(t *testing.T) {
	_, err := cache.New(nil)
	require.ErrorContains(t, err, "no store")
}

func TestEveryCacheSharesOneStore(t *testing.T) {
	c, held := caching(t)

	for _, handle := range []string{"shop/prod/QmA", "shop/prod/QmB", "other/prod/QmA"} {
		_, err := c.Open(t.Context(), grant(handle, nil))
		require.NoError(t, err)
	}
	require.Equal(t, 1, held.asked, "three caches, one store")
}

func TestOpeningTheSameHandleTwiceSeedsItOnce(t *testing.T) {
	c, held := caching(t)

	first, err := c.Open(t.Context(), grant("shop/prod/QmA", nil))
	require.NoError(t, err)
	require.NoError(t, first.Put(t.Context(), "k", []byte("v")))

	again, err := c.Open(t.Context(), grant("shop/prod/QmA", nil))
	require.NoError(t, err)

	require.Equal(t, []string{"shop/prod/QmA/"}, held.store.seeds(),
		"one open, one seed -- and the prefix is the handle")

	// And it is the same accounting, which is what being the same cache means.
	value, err := again.Get(t.Context(), "k")
	require.NoError(t, err)
	require.Equal(t, "v", string(value))
}

func TestTwoHandlesDoNotSeeEachOther(t *testing.T) {
	c, _ := caching(t)
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

func TestTwoDeploymentsOfOneCacheAreSeparate(t *testing.T) {
	c, _ := caching(t)
	ctx := t.Context()

	prod, err := c.Open(ctx, grant("shop/prod/QmA", nil))
	require.NoError(t, err)
	staging, err := c.Open(ctx, grant("shop/staging/QmA", nil))
	require.NoError(t, err)

	require.NoError(t, prod.Put(ctx, "k", []byte("live")))
	_, err = staging.Get(ctx, "k")
	require.Error(t, err, "staging cannot read production's")
}

func TestTheDeclaredSizeIsEnforced(t *testing.T) {
	c, _ := caching(t)
	ctx := t.Context()

	held, err := c.Open(ctx, grant("shop/prod/QmA", map[string]string{"size": "20"}))
	require.NoError(t, err)

	require.NoError(t, held.Put(ctx, "a", []byte("012345678")))
	require.NoError(t, held.Put(ctx, "b", []byte("012345678")))
	require.NoError(t, held.Put(ctx, "c", []byte("012345678")), "it evicted rather than refusing")

	names, err := held.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, names, 2, "the ceiling held")
}

func TestTheDeclaredLifetimeIsRead(t *testing.T) {
	c, _ := caching(t)
	ctx := t.Context()

	brief, err := c.Open(ctx, grant("shop/prod/QmBrief", map[string]string{"ttl": "1ns"}))
	require.NoError(t, err)
	require.NoError(t, brief.Put(ctx, "k", []byte("v")))
	_, err = brief.Get(ctx, "k")
	require.ErrorIs(t, err, wasmtier.ErrNotFound, "a lifetime of a nanosecond has passed")

	long, err := c.Open(ctx, grant("shop/prod/QmLong", map[string]string{"ttl": "24h0m0s"}))
	require.NoError(t, err)
	require.NoError(t, long.Put(ctx, "k", []byte("v")))
	value, err := long.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "v", string(value))
}

func TestAnUnparseableSettingIsNoLimit(t *testing.T) {
	c, _ := caching(t)
	ctx := t.Context()

	held, err := c.Open(ctx, grant("shop/prod/QmA", map[string]string{
		"size": "32MB", // the composer writes bytes; a human string is not one
		"ttl":  "a while",
	}))
	require.NoError(t, err)

	for _, key := range []string{"a", "b", "c"} {
		require.NoError(t, held.Put(ctx, key, []byte("0123456789")))
	}
	names, err := held.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, names, 3, "nothing was evicted, because nothing declared a ceiling")

	value, err := held.Get(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, "0123456789", string(value), "and nothing expired either")
}

func TestAStoreThatCannotBeOpenedIsReportedAndRetried(t *testing.T) {
	held := &opener{store: newRows(), err: errors.New("no database factory")}
	c, err := cache.New(held)
	require.NoError(t, err)

	_, err = c.Open(t.Context(), grant("shop/prod/QmA", nil))
	require.ErrorContains(t, err, "no database factory")

	held.err = nil
	_, err = c.Open(t.Context(), grant("shop/prod/QmA", nil))
	require.NoError(t, err)
	require.Equal(t, 2, held.asked, "the failure was not cached")
}

func TestACacheThatCannotBeSeededIsRefused(t *testing.T) {
	held := &opener{store: newRows()}
	held.store.listErr = errors.New("the store is unreachable")

	c, err := cache.New(held)
	require.NoError(t, err)

	_, err = c.Open(t.Context(), grant("shop/prod/QmA", nil))
	require.ErrorContains(t, err, "unreachable")
}

func TestPkgCacheSentinelsBecomeTierSentinels(t *testing.T) {
	c, _ := caching(t)
	ctx := t.Context()

	held, err := c.Open(ctx, grant("shop/prod/QmA", map[string]string{"size": "10"}))
	require.NoError(t, err)

	_, err = held.Get(ctx, "never-written")
	require.ErrorIs(t, err, wasmtier.ErrNotFound, "a miss is the tier's miss")
	require.ErrorIs(t, err, pkgcache.ErrNotFound, "and the cause is still readable in a log")

	err = held.Put(ctx, "k", make([]byte, 100))
	require.ErrorIs(t, err, wasmtier.ErrTooLarge)
	require.ErrorIs(t, err, pkgcache.ErrTooLarge)

	// And the SECOND open of the same handle answers the same way — the cached
	// path returned the raw cache once, which is how this was found.
	again, err := c.Open(ctx, grant("shop/prod/QmA", nil))
	require.NoError(t, err)
	_, err = again.Get(ctx, "never-written")
	require.ErrorIs(t, err, wasmtier.ErrNotFound, "the reused cache is wrapped too")
}

func TestAnUnrecognisedFailureIsNotTranslated(t *testing.T) {
	c, held := caching(t)
	ctx := t.Context()

	cached, err := c.Open(ctx, grant("shop/prod/QmA", nil))
	require.NoError(t, err)

	unreachable := errors.New("the swarm is unreachable")
	held.store.putErr = unreachable

	err = cached.Put(ctx, "k", []byte("v"))
	require.ErrorIs(t, err, unreachable)
	require.NotErrorIs(t, err, wasmtier.ErrNotFound)
	require.NotErrorIs(t, err, wasmtier.ErrTooLarge)

	held.store.putErr = nil
	require.NoError(t, cached.Put(ctx, "k", []byte("v")))

	held.store.getErr = unreachable
	_, err = cached.Get(ctx, "k")
	require.ErrorIs(t, err, unreachable)
	require.NotErrorIs(t, err, wasmtier.ErrNotFound, "a store that could not look is not a miss")
}

func TestDeleteAndListComeBackThroughTheTranslation(t *testing.T) {
	c, _ := caching(t)
	ctx := t.Context()

	held, err := c.Open(ctx, grant("shop/prod/QmA", nil))
	require.NoError(t, err)

	require.NoError(t, held.Delete(ctx, "never-written"))
	require.NoError(t, held.Put(ctx, "k", []byte("v")))
	require.NoError(t, held.Delete(ctx, "k"))

	names, err := held.List(ctx, "")
	require.NoError(t, err)
	require.Empty(t, names)
}
