package cache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// clock is a hand-wound time source, so an entry can be aged without sleeping.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func newClock() *clock { return &clock{at: time.Unix(1700000000, 0).UTC()} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func open(t *testing.T, store Store, cfg Config, clk *clock) *Cache {
	t.Helper()
	if cfg.Prefix == "" {
		cfg.Prefix = "shop/prod/QmCache"
	}
	c, err := Open(t.Context(), store, cfg, WithClock(clk.now))
	require.NoError(t, err)
	return c
}

func TestACacheWithNoStoreHoldsNothing(t *testing.T) {
	_, err := Open(context.Background(), nil, Config{})
	require.ErrorIs(t, err, ErrNoStore)
}

func TestPutGetDelete(t *testing.T) {
	c := open(t, NewMemStore(), Config{}, newClock())
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "session:1", []byte("alice")))

	value, err := c.Get(ctx, "session:1")
	require.NoError(t, err)
	require.Equal(t, "alice", string(value))

	require.NoError(t, c.Delete(ctx, "session:1"))
	_, err = c.Get(ctx, "session:1")
	require.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, c.Delete(ctx, "session:1"), "deleting what is gone succeeds")
}

func TestAMissIsNotAnError(t *testing.T) {
	c := open(t, NewMemStore(), Config{}, newClock())

	_, err := c.Get(t.Context(), "never-written")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAnEmptyKeyIsRefused(t *testing.T) {
	c := open(t, NewMemStore(), Config{}, newClock())
	ctx := t.Context()

	_, err := c.Get(ctx, "")
	require.ErrorIs(t, err, ErrNoKey)
	require.ErrorIs(t, c.Put(ctx, "", nil), ErrNoKey)
	require.ErrorIs(t, c.Delete(ctx, ""), ErrNoKey)
}

func TestAnExpiredEntryIsAMissAndIsRemoved(t *testing.T) {
	clk := newClock()
	store := NewMemStore()
	c := open(t, store, Config{TTL: time.Minute}, clk)
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "k", []byte("v")))
	require.Equal(t, 1, store.Len())

	clk.advance(time.Minute)
	_, err := c.Get(ctx, "k")
	require.ErrorIs(t, err, ErrNotFound)

	require.Zero(t, store.Len(), "the row is gone, not just the answer")
	require.Zero(t, c.Used(), "and so are its bytes")
}

func TestAnEntryWithNoTTLDoesNotExpire(t *testing.T) {
	clk := newClock()
	c := open(t, NewMemStore(), Config{}, clk)
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "k", []byte("v")))
	clk.advance(1000 * time.Hour)

	value, err := c.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "v", string(value))
}

func TestAnEntryIsLiveUntilItsDeadline(t *testing.T) {
	clk := newClock()
	c := open(t, NewMemStore(), Config{TTL: time.Minute}, clk)
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "k", []byte("v")))

	clk.advance(59 * time.Second)
	_, err := c.Get(ctx, "k")
	require.NoError(t, err, "still inside its lifetime")

	clk.advance(time.Second)
	_, err = c.Get(ctx, "k")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestListHidesExpiredEntriesWithoutRemovingThem(t *testing.T) {
	clk := newClock()
	store := NewMemStore()
	c := open(t, store, Config{TTL: time.Minute}, clk)
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "a", []byte("1")))
	require.NoError(t, c.Put(ctx, "b", []byte("2")))

	names, err := c.List(ctx, "")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, names)

	clk.advance(time.Minute)
	names, err = c.List(ctx, "")
	require.NoError(t, err)
	require.Empty(t, names)
	require.Equal(t, 2, store.Len(), "a read did not write")
}

func TestListIsScopedByPrefixAndSorted(t *testing.T) {
	c := open(t, NewMemStore(), Config{}, newClock())
	ctx := t.Context()

	for _, k := range []string{"user:2", "user:1", "order:9"} {
		require.NoError(t, c.Put(ctx, k, []byte("v")))
	}

	names, err := c.List(ctx, "user:")
	require.NoError(t, err)
	require.Equal(t, []string{"user:1", "user:2"}, names)
}

func TestAFullCacheEvictsTheOldest(t *testing.T) {
	clk := newClock()
	// Each entry is 1 key byte + 9 value bytes = 10; the ceiling holds two.
	c := open(t, NewMemStore(), Config{Size: 20}, clk)
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "a", []byte("012345678")))
	clk.advance(time.Second)
	require.NoError(t, c.Put(ctx, "b", []byte("012345678")))
	require.Equal(t, int64(20), c.Used())

	clk.advance(time.Second)
	require.NoError(t, c.Put(ctx, "c", []byte("012345678")), "it made room instead of refusing")

	_, err := c.Get(ctx, "a")
	require.ErrorIs(t, err, ErrNotFound, "the oldest went")
	for _, k := range []string{"b", "c"} {
		_, err := c.Get(ctx, k)
		require.NoError(t, err, k)
	}
	require.Equal(t, int64(20), c.Used())
}

func TestEvictionTakesExpiredEntriesFirst(t *testing.T) {
	clk := newClock()
	c := open(t, NewMemStore(), Config{Size: 20, TTL: 10 * time.Second}, clk)
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "a", []byte("012345678")))
	clk.advance(20 * time.Second) // a is now expired
	require.NoError(t, c.Put(ctx, "b", []byte("012345678")))

	// b is the NEWEST, and a is expired: a must be the one that goes.
	require.NoError(t, c.Put(ctx, "c", []byte("012345678")))

	_, err := c.Get(ctx, "b")
	require.NoError(t, err, "the live entry survived")
	_, err = c.Get(ctx, "a")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestOverwritingDoesNotEvict(t *testing.T) {
	c := open(t, NewMemStore(), Config{Size: 20}, newClock())
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "a", []byte("012345678")))
	require.NoError(t, c.Put(ctx, "b", []byte("012345678")))
	require.NoError(t, c.Put(ctx, "a", []byte("987654321")))

	for _, k := range []string{"a", "b"} {
		_, err := c.Get(ctx, k)
		require.NoError(t, err, k)
	}
	require.Equal(t, int64(20), c.Used())
}

func TestAValueLargerThanTheCacheIsRefusedWithoutEmptyingIt(t *testing.T) {
	c := open(t, NewMemStore(), Config{Size: 20}, newClock())
	ctx := t.Context()

	require.NoError(t, c.Put(ctx, "a", []byte("012345678")))

	err := c.Put(ctx, "big", make([]byte, 100))
	require.ErrorIs(t, err, ErrTooLarge)

	_, err = c.Get(ctx, "a")
	require.NoError(t, err, "the cache was not emptied to make room for something that never fits")
}

func TestACacheWithNoCeilingHoldsWhateverItIsGiven(t *testing.T) {
	c := open(t, NewMemStore(), Config{}, newClock())
	ctx := t.Context()

	for i := range 50 {
		require.NoError(t, c.Put(ctx, fmt.Sprintf("k%d", i), make([]byte, 1000)))
	}
	names, err := c.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, names, 50)
}

func TestAKeyCountsTowardTheCeiling(t *testing.T) {
	c := open(t, NewMemStore(), Config{Size: 10}, newClock())
	require.NoError(t, c.Put(t.Context(), strings.Repeat("k", 10), nil))
	require.Equal(t, int64(10), c.Used())
}

func TestReopeningCountsWhatTheStoreAlreadyHolds(t *testing.T) {
	clk := newClock()
	store := NewMemStore()

	first := open(t, store, Config{Size: 20}, clk)
	require.NoError(t, first.Put(t.Context(), "a", []byte("012345678")))
	require.NoError(t, first.Put(t.Context(), "b", []byte("012345678")))

	again := open(t, store, Config{Size: 20}, clk)
	require.Equal(t, int64(20), again.Used(), "it counted what was there")

	// And the entries are readable across the reopen.
	value, err := again.Get(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, "012345678", string(value))
}

func TestTwoCachesInOneStoreDoNotSeeEachOther(t *testing.T) {
	store := NewMemStore()
	clk := newClock()

	mine := open(t, store, Config{Prefix: "shop/prod/QmA"}, clk)
	theirs := open(t, store, Config{Prefix: "shop/prod/QmB"}, clk)
	ctx := t.Context()

	require.NoError(t, mine.Put(ctx, "k", []byte("mine")))
	require.NoError(t, theirs.Put(ctx, "k", []byte("theirs")))

	value, err := mine.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "mine", string(value))

	names, err := theirs.List(ctx, "")
	require.NoError(t, err)
	require.Equal(t, []string{"k"}, names)
}

func TestAPrefixCannotReachALongerOne(t *testing.T) {
	store := NewMemStore()
	clk := newClock()
	ctx := t.Context()

	long := open(t, store, Config{Prefix: "shop/prod/QmAB"}, clk)
	require.NoError(t, long.Put(ctx, "k", []byte("v")))

	short := open(t, store, Config{Prefix: "shop/prod/QmA"}, clk)
	require.Zero(t, short.Used())
	names, err := short.List(ctx, "")
	require.NoError(t, err)
	require.Empty(t, names)
}

var errUnreachable = errors.New("the store is unreachable")

type brokenStore struct {
	Store
	failGet, failPut, failDelete, failList bool
}

func (b brokenStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if b.failGet {
		return nil, false, errUnreachable
	}
	return b.Store.Get(ctx, key)
}

func (b brokenStore) Put(ctx context.Context, key string, value []byte) error {
	if b.failPut {
		return errUnreachable
	}
	return b.Store.Put(ctx, key, value)
}

func (b brokenStore) Delete(ctx context.Context, key string) error {
	if b.failDelete {
		return errUnreachable
	}
	return b.Store.Delete(ctx, key)
}

func (b brokenStore) List(ctx context.Context, prefix string) ([]string, error) {
	if b.failList {
		return nil, errUnreachable
	}
	return b.Store.List(ctx, prefix)
}

func TestAStoreThatCannotAnswerIsReportedAndNotTreatedAsAMiss(t *testing.T) {
	ctx := context.Background()

	_, err := Open(ctx, brokenStore{Store: NewMemStore(), failList: true}, Config{Prefix: "p"})
	require.ErrorIs(t, err, errUnreachable)

	c, err := Open(ctx, brokenStore{Store: NewMemStore(), failGet: true}, Config{Prefix: "p"})
	require.NoError(t, err, "an empty listing seeded nothing to read")
	_, err = c.Get(ctx, "k")
	require.ErrorIs(t, err, errUnreachable)
	require.NotErrorIs(t, err, ErrNotFound, "unreachable is not a miss")

	c, err = Open(ctx, brokenStore{Store: NewMemStore(), failPut: true}, Config{Prefix: "p"})
	require.NoError(t, err)
	require.ErrorIs(t, c.Put(ctx, "k", []byte("v")), errUnreachable)
	require.Zero(t, c.Used(), "a write that failed was not counted")

	c, err = Open(ctx, brokenStore{Store: NewMemStore(), failDelete: true}, Config{Prefix: "p"})
	require.NoError(t, err)
	require.ErrorIs(t, c.Delete(ctx, "k"), errUnreachable)
}

func TestASeedSkipsARowItCannotRead(t *testing.T) {
	store := NewMemStore()
	require.NoError(t, store.Put(context.Background(), "shop/prod/QmCache/junk", []byte("short")))

	c := open(t, store, Config{}, newClock())
	require.Zero(t, c.Used())

	_, err := c.Get(t.Context(), "junk")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestConcurrentWritesKeepTheAccountingHonest(t *testing.T) {
	c := open(t, NewMemStore(), Config{}, newClock())
	ctx := t.Context()

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, c.Put(ctx, fmt.Sprintf("k%02d", i), []byte("value")))
		}()
	}
	wg.Wait()

	// 50 entries, each 3 key bytes + 5 value bytes.
	require.Equal(t, int64(50*8), c.Used())
}
