package cache_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node/cache"
	pkgcache "github.com/taubyte/tau/pkg/cache"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// cache_test is the reason this package exists.
//
// Every one of these ran a whole node when the adapter took *Node — a mocknet
// host, a gossipsub, a register and a kvdb factory — to assert that opening one
// handle twice seeds it once. One interface later the backing is a map, and the
// cases that were awkward to reach — a store that fails the first time and
// works the next, a seed that cannot be read — are ordinary rows.
//
// THE CACHES THEMSELVES ARE REAL. pkg/cache is what enforces the ceiling and
// the lifetime, and what raises the two sentinels this adapter translates; a
// double for it would leave the translation asserting against itself.

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

// --- construction -----------------------------------------------------------

func TestTheAdapterNeedsSomewhereToPutRows(t *testing.T) {
	_, err := cache.New(nil)
	require.ErrorContains(t, err, "no store")
}

// --- one database, one seed -------------------------------------------------

// TestONEDATABASEHOLDSEVERYCACHE. A kvdb per cache would be a CRDT, a
// broadcaster and a pubsub topic per resource; one store with the handle as the
// prefix is the same isolation with one set of moving parts.
func TestEveryCacheSharesOneStore(t *testing.T) {
	c, held := caching(t)

	for _, handle := range []string{"shop/prod/QmA", "shop/prod/QmB", "other/prod/QmA"} {
		_, err := c.Open(t.Context(), grant(handle, nil))
		require.NoError(t, err)
	}
	require.Equal(t, 1, held.asked, "three caches, one store")
}

// TestANOPENEDCACHEISKEPT, because pkg/cache scans what this node already holds
// when it opens and paying that per invocation would put it on the request path.
// The scan is the assertion: a second open that seeded again would list twice.
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

// TestTWOHANDLESARETWOCACHES, and the check that matters is that one cannot
// read the other.
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

// TestANENVIRONMENTISITSOWNDATA. One declaration, two deployments: staging's
// entries and production's are the same cache in the config and different rows
// in the store.
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

// --- what the target declared -----------------------------------------------

// TestTHEDECLAREDCEILINGISENFORCED, from the settings the grant carried rather
// than from a lookup this backing would have to make on the request path.
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

// TestTHEDECLAREDLIFETIMEREACHESTHECACHE, which is asserted by an entry that is
// already gone rather than by parsing the setting twice.
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

// TestANUNREADABLESETTINGISNOLIMITRATHERTHANAREFUSAL. A cache with no ceiling
// is a bug to find in a log; one that refuses every write is an outage.
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

// --- a store that is not there ----------------------------------------------

// TestASTORETHATCANNOTBEOPENEDISREPORTEDBYNAME, which on a real node is an
// operator who gave it no database factory -- and it is asked again on the next
// request rather than being remembered as broken, because the thing that failed
// is outside this package.
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

// TestASEEDTHATCANNOTBEREADISNOTANEMPTYCACHE. Starting empty would make the
// first writes after it free, so the ceiling would hold twice as much.
func TestACacheThatCannotBeSeededIsRefused(t *testing.T) {
	held := &opener{store: newRows()}
	held.store.listErr = errors.New("the store is unreachable")

	c, err := cache.New(held)
	require.NoError(t, err)

	_, err = c.Open(t.Context(), grant("shop/prod/QmA", nil))
	require.ErrorContains(t, err, "unreachable")
}

// --- the sentinels the tier acts on -----------------------------------------

// TestAMISSARRIVESASTHETIERSMISS, WHICH THREE PASSING UNIT TESTS DID NOT CATCH.
//
// pkg/cache has its own ErrNotFound and pkg/invoke/wasm has another; the tier
// maps anything it does not recognise onto the guest's `unavailable` arm. So a
// cache miss — the common case, the thing a cache is FOR — was reported to
// every guest as an outage, and an author would have written a retry around it.
// Each package was right on its own and the composition was wrong.
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

// TestANYTHINGELSEISLEFTALONE, so a store that could not answer stays
// distinguishable from one that had nothing to say.
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

// TestDELETINGWHATISNOTTHERESUCCEEDS, and a delete is translated like the rest.
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
