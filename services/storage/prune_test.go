package storage

import (
	"testing"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/require"
)

// stampAt is a stamp minted at ms.
func stampAt(t *testing.T, ms int64) uint64 {
	t.Helper()
	st, err := PackStamp(uint64(ms), 0)
	require.NoError(t, err)
	return st
}

// ROWS NO READER IS OWED GO, AND NONE OTHER: a superseded row its bucket does not keep
// and an expired one, each once pruneAfter has passed; a name's newest row only once
// it is a tombstone or expired and every older row goes too; anything newer, kept, or
// another kind of row, stays.
func TestPruningRemovesOnlyRowsNoReaderIsOwed(t *testing.T) {
	eachBackend(t, func(t *testing.T, store Datastore) {
		ctx := t.Context()
		now := time.UnixMilli(1_800_000_000_000)
		long, recent := now.Add(-72*time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli()
		after := 48 * time.Hour
		i := index{store: store}
		a := addr(t)
		root := testCID(t, "bytes").String()
		put := func(name string, ms int64, rec VersionRecord) string {
			t.Helper()
			rec.Writer = "w"
			if !rec.Tombstone {
				rec.RootCID = root
			}
			v, err := i.Append(ctx, a, name, stampAt(t, ms), "w", rec)
			require.NoError(t, err)
			key, err := a.versionKey(name, v.Stamp, "w")
			require.NoError(t, err)
			return key
		}
		gone, kept := map[string]bool{}, map[string]bool{}
		mark := func(goes bool, key string) {
			if goes {
				gone[key] = true
			} else {
				kept[key] = true
			}
		}

		// Superseded long ago, not kept: the old row goes, the current stays.
		mark(true, put("overwritten", long-1, VersionRecord{}))
		mark(false, put("overwritten", long, VersionRecord{}))
		// Superseded long ago, kept: both stay.
		mark(false, put("versioned", long-1, VersionRecord{Kept: true}))
		mark(false, put("versioned", long, VersionRecord{Kept: true}))
		// Superseded recently: both stay.
		mark(false, put("fresh", long, VersionRecord{}))
		mark(false, put("fresh", recent, VersionRecord{}))
		// Deleted long ago: the version and the tombstone go.
		mark(true, put("deleted", long-1, VersionRecord{}))
		mark(true, put("deleted", long, VersionRecord{Tombstone: true}))
		// Deleted long ago under versioning: the kept version stays, so the
		// tombstone over it does.
		mark(false, put("deleted-kept", long-1, VersionRecord{Kept: true}))
		mark(false, put("deleted-kept", long, VersionRecord{Tombstone: true}))
		// Deleted recently: both stay.
		mark(false, put("just-deleted", long, VersionRecord{}))
		mark(false, put("just-deleted", recent, VersionRecord{Tombstone: true}))
		// Expired long ago, kept and current: it goes.
		mark(true, put("expired", long-1, VersionRecord{Kept: true, ExpiresMS: uint64(long)}))
		// Expired recently: it stays.
		mark(false, put("expiring", long, VersionRecord{ExpiresMS: uint64(recent)}))
		// An expired current row over a live kept one stays: removing it would bring
		// the older one back.
		mark(false, put("outlived", long-1, VersionRecord{Kept: true}))
		mark(false, put("outlived", long, VersionRecord{ExpiresMS: uint64(long)}))

		usage, err := a.usageKey("w", usageAdded)
		require.NoError(t, err)
		require.NoError(t, store.Put(ctx, ds.RawKey(usage), encodeMS(1)))
		kept[usage] = true

		i.at = now
		n, err := i.Prune(ctx, after)
		require.NoError(t, err)
		require.Equal(t, len(gone), n)
		for key := range gone {
			has, err := store.Has(ctx, ds.RawKey(key))
			require.NoError(t, err)
			require.False(t, has, "%s is owed to no reader and was kept", key)
		}
		for key := range kept {
			has, err := store.Has(ctx, ds.RawKey(key))
			require.NoError(t, err)
			require.True(t, has, "%s was pruned", key)
		}

		n, err = i.Prune(ctx, after)
		require.NoError(t, err)
		require.Zero(t, n, "a second prune removed more")

		_, err = index{store: store}.Prune(ctx, after)
		require.Error(t, err, "a prune with no instant to prune at")
	})
}

// A PRUNE THE STORE FAILS IS AN ERROR, never a partial success said to be whole.
func TestAPruneTheStoreFailsIsAnError(t *testing.T) {
	_, err := (index{store: failingStore{}, at: time.Now()}).Prune(t.Context(), time.Hour)
	require.ErrorIs(t, err, errInjected)
}

// A NODE PRUNES THE SHARDS IT HOLDS once every resweep.
func TestANodePrunesItsShardsOnceAResweep(t *testing.T) {
	at := assetsOf(t, "site_code")
	clk := newFixedClock(1_700_000_000_000)
	c := newCluster(t, 3, fixedCatalog{at}, withClock(clk.now))
	putOn(t, c, at, "page", "the first page", Object{})
	putOn(t, c, at, "page", "the second page", Object{})
	n := c.nodes[0]
	rows := func() int {
		t.Helper()
		vs, err := (index{store: shardOn(t, c, 0, at)}).Versions(t.Context(), at, "page", 0)
		require.NoError(t, err)
		return len(vs)
	}

	clk.advance(n.pruneAfter())
	require.NoError(t, n.pruneIfDue(t.Context()))
	require.Equal(t, 1, rows(), "a superseded row past pruneAfter was kept")

	putOn(t, c, at, "page", "the third page", Object{})
	clk.advance(n.pruneAfter())
	require.NoError(t, n.pruneIfDue(t.Context()))
	require.Equal(t, 1, rows())
	clk.advance(resweep - time.Second)
	putOn(t, c, at, "page", "the fourth page", Object{})
	require.NoError(t, n.pruneIfDue(t.Context()))
	require.Equal(t, 2, rows(), "a prune ran before resweep")
}
