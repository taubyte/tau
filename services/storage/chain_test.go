package storage

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/require"
)

func rec(cidStr string, size uint64, writer string) VersionRecord {
	return VersionRecord{RootCID: cidStr, Size: size, Writer: writer, CreatedMS: 1_700_000_000_000}
}

func tomb(writer string) VersionRecord {
	return VersionRecord{Writer: writer, Tombstone: true, CreatedMS: 1_700_000_000_000}
}

func TestCurrentIsTheNewestRowOnEveryBackend(t *testing.T) {
	eachBackend(t, func(t *testing.T, store Datastore) {
		ctx := context.Background()
		i := index{store: store}
		a := addr(t)

		for _, s := range []uint64{1, 2, 3, 10, 100} {
			_, err := i.Append(ctx, a, "photo.jpg", s, "peerA", rec(fmt.Sprintf("cid-%d", s), s, "peerA"))
			require.NoError(t, err)
		}

		v, err := i.Current(ctx, a, "photo.jpg")
		require.NoError(t, err)
		require.Equal(t, uint64(100), v.Stamp)
		require.Equal(t, "cid-100", v.Record.RootCID)
		require.Equal(t, "peerA", v.Writer)

		// The full list is descending, the keys' own order.
		vs, err := i.Versions(ctx, a, "photo.jpg", 0)
		require.NoError(t, err)
		require.Equal(t, []uint64{100, 10, 3, 2, 1}, stamps(vs))
	})
}

func TestTwoWritersNamingOneKeyBothSurvive(t *testing.T) {
	eachBackend(t, func(t *testing.T, store Datastore) {
		ctx := context.Background()
		i := index{store: store}
		a := addr(t)

		// Same object, same stamp, two nodes. This is the concurrent case.
		_, err := i.Append(ctx, a, "photo.jpg", 100, "peerA", rec("cid-A", 1, "peerA"))
		require.NoError(t, err)
		_, err = i.Append(ctx, a, "photo.jpg", 100, "peerB", rec("cid-B", 2, "peerB"))
		require.NoError(t, err)

		vs, err := i.Versions(ctx, a, "photo.jpg", 0)
		require.NoError(t, err)
		require.Len(t, vs, 2, "neither write may be discarded")

		// And every replica resolves the same one, by byte order of the key.
		v, err := i.Current(ctx, a, "photo.jpg")
		require.NoError(t, err)
		require.Equal(t, vs[0].Writer, v.Writer)

		// The loser is still retrievable, which is what "not discarded" has to
		// mean for it to be worth anything.
		both, err := i.At(ctx, a, "photo.jpg", 100)
		require.NoError(t, err)
		require.Len(t, both, 2)
		require.Equal(t, both[0].Writer, v.Writer)
	})
}

func TestTheKeyOrderAndTheResolutionOrderAreOneOrder(t *testing.T) {
	a := addr(t)
	rng := rand.New(rand.NewSource(21))

	for trial := 0; trial < 300; trial++ {
		n := 2 + rng.Intn(8)
		var versions []Version
		var keys []string
		for k := 0; k < n; k++ {
			// Few distinct stamps, so ties are the common case rather than
			// the rare one.
			stamp := uint64(rng.Intn(3))
			writer := randomBytesName(rng)
			key, err := a.versionKey("obj", stamp, writer)
			require.NoError(t, err)
			if containsStr(keys, key) {
				continue
			}
			keys = append(keys, key)
			vk, err := ParseVersionKey(key)
			require.NoError(t, err)
			versions = append(versions, Version{VersionKey: vk, Record: rec("c", 1, writer)})
		}

		sort.Strings(keys)
		ordered := Order(versions)
		require.Len(t, ordered, len(keys))
		for idx, v := range ordered {
			want, err := a.versionKey(v.Name, v.Stamp, v.Writer)
			require.NoError(t, err)
			require.Equal(t, keys[idx], want, "resolution order disagrees with key order at %d", idx)
		}

		winner, ok := Newest(versions)
		require.True(t, ok)
		require.Equal(t, ordered[0], winner)
	}

	_, ok := Newest(nil)
	require.False(t, ok)
}

func TestOrderDoesNotReorderItsCaller(t *testing.T) {
	a := addr(t)
	var vs []Version
	for _, s := range []uint64{1, 5, 3} {
		key, err := a.versionKey("obj", s, "w")
		require.NoError(t, err)
		vk, err := ParseVersionKey(key)
		require.NoError(t, err)
		vs = append(vs, Version{VersionKey: vk, Record: rec("c", 1, "w")})
	}
	before := append([]Version(nil), vs...)
	Order(vs)
	Newest(vs)
	require.Equal(t, before, vs)
}

func TestADeleteIsARowAndARaceWithAWriteLosesNothing(t *testing.T) {
	eachBackend(t, func(t *testing.T, store Datastore) {
		ctx := context.Background()
		i := index{store: store}
		a := addr(t)

		_, err := i.Append(ctx, a, "doc.txt", 10, "peerA", rec("cid-1", 5, "peerA"))
		require.NoError(t, err)

		// A delete and a re-create, concurrent, from two nodes, at the same
		// stamp: both land.
		_, err = i.Append(ctx, a, "doc.txt", 20, "peerA", tomb("peerA"))
		require.NoError(t, err)
		_, err = i.Append(ctx, a, "doc.txt", 20, "peerB", rec("cid-2", 7, "peerB"))
		require.NoError(t, err)

		vs, err := i.Versions(ctx, a, "doc.txt", 0)
		require.NoError(t, err)
		require.Len(t, vs, 3, "nothing is lost: the delete and the re-create are two rows")

		// The winner is whichever the byte order says, identically everywhere.
		v, err := i.Current(ctx, a, "doc.txt")
		if vs[0].Tombstone() {
			require.ErrorIs(t, err, ErrNotFound, "a tombstone resolves as the object being gone")
		} else {
			require.NoError(t, err)
			require.Equal(t, vs[0].Writer, v.Writer)
		}

		// A LATER re-create always wins over the tombstone, visibly.
		_, err = i.Append(ctx, a, "doc.txt", 30, "peerA", rec("cid-3", 9, "peerA"))
		require.NoError(t, err)
		v, err = i.Current(ctx, a, "doc.txt")
		require.NoError(t, err)
		require.Equal(t, "cid-3", v.Record.RootCID)

		// And a later delete hides it again, with the history intact.
		_, err = i.Append(ctx, a, "doc.txt", 40, "peerA", tomb("peerA"))
		require.NoError(t, err)
		_, err = i.Current(ctx, a, "doc.txt")
		require.ErrorIs(t, err, ErrNotFound)

		vs, err = i.Versions(ctx, a, "doc.txt", 0)
		require.NoError(t, err)
		require.Len(t, vs, 5, "a delete never removes a row")
		require.True(t, vs[0].Tombstone())
	})
}

func TestAKvdbDeleteWouldHaveDiscardedOne(t *testing.T) {
	ctx := context.Background()
	store := newOrderedMem()
	a := addr(t)

	pointer, err := a.objectPrefix("doc.txt")
	require.NoError(t, err)

	require.NoError(t, store.Put(ctx, ds.RawKey(pointer), []byte("cid-1")))
	require.NoError(t, store.Delete(ctx, ds.RawKey(pointer)))
	require.NoError(t, store.Put(ctx, ds.RawKey(pointer), []byte("cid-2")))

	// One key, so the delete and the re-create are three writes and one
	// survivor, with no record that the other two happened.
	_, err = store.Get(ctx, ds.RawKey(pointer))
	require.NoError(t, err)
	res, err := store.Query(ctx, queryPrefix(pointer))
	require.NoError(t, err)
	rest, err := res.Rest()
	require.NoError(t, err)
	require.Empty(t, rest, "nothing under the pointer: the history does not exist")
}

func TestARetryIsANoOpMerge(t *testing.T) {
	eachBackend(t, func(t *testing.T, store Datastore) {
		ctx := context.Background()
		i := index{store: store}
		a := addr(t)

		r := rec("cid-1", 5, "peerA")
		for attempt := 0; attempt < 4; attempt++ {
			_, err := i.Append(ctx, a, "doc.txt", 10, "peerA", r)
			require.NoError(t, err)
		}
		vs, err := i.Versions(ctx, a, "doc.txt", 0)
		require.NoError(t, err)
		require.Len(t, vs, 1, "the same (name, stamp, writer) is the same key")
	})
}

func TestVersionsRespectsALimitAndAtFindsOneStamp(t *testing.T) {
	ctx := context.Background()
	i := index{store: newOrderedMem()}
	a := addr(t)

	for _, s := range []uint64{1, 2, 3, 4, 5} {
		_, err := i.Append(ctx, a, "obj", s, "peerA", rec(fmt.Sprint(s), s, "peerA"))
		require.NoError(t, err)
	}
	vs, err := i.Versions(ctx, a, "obj", 2)
	require.NoError(t, err)
	require.Equal(t, []uint64{5, 4}, stamps(vs))

	at, err := i.At(ctx, a, "obj", 3)
	require.NoError(t, err)
	require.Len(t, at, 1)
	require.Equal(t, uint64(3), at[0].Stamp)

	_, err = i.At(ctx, a, "obj", 99)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAMissingObjectIsNotFound(t *testing.T) {
	eachBackend(t, func(t *testing.T, store Datastore) {
		ctx := context.Background()
		i := index{store: store}
		a := addr(t)

		_, err := i.Current(ctx, a, "never-written")
		require.ErrorIs(t, err, ErrNotFound)
		vs, err := i.Versions(ctx, a, "never-written", 0)
		require.NoError(t, err)
		require.Empty(t, vs)
	})
}

func TestARecordThatDisagreesWithItsKeyIsRefused(t *testing.T) {
	ctx := context.Background()
	store := newOrderedMem()
	i := index{store: store}
	a := addr(t)

	// Appending under one writer with a record naming another is refused at
	// the door.
	_, err := i.Append(ctx, a, "obj", 1, "peerA", rec("c", 1, "peerB"))
	require.ErrorIs(t, err, ErrWriterMismatch)

	// And a row already in the store that says it is refused on read, rather
	// than resolved as if it were a conflict.
	key, err := a.versionKey("obj", 1, "peerA")
	require.NoError(t, err)
	raw, err := rec("c", 1, "peerB").encode()
	require.NoError(t, err)
	require.NoError(t, store.Put(ctx, ds.RawKey(key), raw))

	_, err = i.Current(ctx, a, "obj")
	require.ErrorIs(t, err, ErrWriterMismatch)

	// A record with no writer at all takes the key's, because the key is
	// authoritative.
	blank, err := VersionRecord{RootCID: "c"}.encode()
	require.NoError(t, err)
	require.NoError(t, store.Put(ctx, ds.RawKey(key), blank))
	v, err := i.Current(ctx, a, "obj")
	require.NoError(t, err)
	require.Equal(t, "peerA", v.Writer)
}

func TestAppendFillsTheWritersName(t *testing.T) {
	ctx := context.Background()
	i := index{store: newOrderedMem()}
	a := addr(t)

	v, err := i.Append(ctx, a, "obj", 1, "peerA", VersionRecord{RootCID: "c", Size: 3})
	require.NoError(t, err)
	require.Equal(t, "peerA", v.Record.Writer)
	require.Equal(t, "peerA", v.Writer)
}

func TestAForeignRowInTheObjectNamespaceIsAnError(t *testing.T) {
	ctx := context.Background()
	store := newOrderedMem()
	i := index{store: store}
	a := addr(t)

	prefix, err := a.objectPrefix("obj")
	require.NoError(t, err)
	require.NoError(t, store.Put(ctx, ds.RawKey(prefix+"/not-a-stamp/w"), []byte("junk")))

	_, err = i.Current(ctx, a, "obj")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	_, err = i.Versions(ctx, a, "obj", 0)
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	_, err = i.At(ctx, a, "obj", 1)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAnUndecodableRecordIsAnError(t *testing.T) {
	ctx := context.Background()
	store := newOrderedMem()
	i := index{store: store}
	a := addr(t)

	key, err := a.versionKey("obj", 1, "peerA")
	require.NoError(t, err)
	require.NoError(t, store.Put(ctx, ds.RawKey(key), []byte{0xff, 0xff, 0xff}))

	_, err = i.Current(ctx, a, "obj")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}

func TestScanNarrowsByPrefixAndResumesAfterAKey(t *testing.T) {
	ctx := context.Background()
	i := index{store: newOrderedMem()}
	a := addr(t)

	names := []string{"photos/2024/a", "photos/2024/z", "photos/2024-old", "photos/2025/b", "readme"}
	for _, n := range names {
		_, err := i.Append(ctx, a, n, 1, "peerA", rec("c", 1, "peerA"))
		require.NoError(t, err)
	}

	objects, err := a.objectsPrefix()
	require.NoError(t, err)
	raw, err := a.listPrefix("photos/2024")
	require.NoError(t, err)

	rows, err := i.scan(ctx, objects, raw, "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"photos/2024-old", "photos/2024/a", "photos/2024/z"}, namesOf(t, rows),
		"exact, and in lexicographic order of the raw object name")

	// A resumed scan never re-reads and never skips.
	page1, err := i.scan(ctx, objects, raw, "", 2)
	require.NoError(t, err)
	require.Len(t, page1, 2)
	page2, err := i.scan(ctx, objects, raw, page1[1].key, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"photos/2024/z"}, namesOf(t, page2))

	// The empty prefix is every object.
	all, err := a.listPrefix("")
	require.NoError(t, err)
	rows, err = i.scan(ctx, objects, all, "", 0)
	require.NoError(t, err)
	require.Len(t, rows, len(names))
}

func TestGetAndPutGoThroughTheIndexDoor(t *testing.T) {
	ctx := context.Background()
	store := newOrderedMem()
	i := index{store: store}
	a := addr(t)

	key, err := a.usageKey("peerA", usageAdded)
	require.NoError(t, err)

	_, err = i.get(ctx, key)
	require.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, i.put(ctx, key, []byte("7")))
	v, err := i.get(ctx, key)
	require.NoError(t, err)
	require.Equal(t, []byte("7"), v)
}

func TestScanPropagatesAStoreFailure(t *testing.T) {
	ctx := context.Background()
	i := index{store: failingStore{}}
	a := addr(t)

	_, err := i.Current(ctx, a, "obj")
	require.ErrorIs(t, err, errInjected)
	_, err = i.Versions(ctx, a, "obj", 0)
	require.ErrorIs(t, err, errInjected)
	_, err = i.At(ctx, a, "obj", 1)
	require.ErrorIs(t, err, errInjected)
	_, err = i.Append(ctx, a, "obj", 1, "w", rec("c", 1, "w"))
	require.ErrorIs(t, err, errInjected)
	_, err = i.get(ctx, "/b/x")
	require.ErrorIs(t, err, errInjected)
}

func TestABadAddressIsRefusedByEveryChainDoor(t *testing.T) {
	ctx := context.Background()
	i := index{store: newOrderedMem()}
	var zero Address

	_, err := i.Current(ctx, zero, "obj")
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = i.Versions(ctx, zero, "obj", 0)
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = i.At(ctx, zero, "obj", 1)
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = i.Append(ctx, zero, "obj", 1, "w", rec("c", 1, "w"))
	require.ErrorIs(t, err, ErrNoAddress)

	// And an unencodable record name, at the same doors.
	a := addr(t)
	_, err = i.Append(ctx, a, "", 1, "w", rec("c", 1, "w"))
	require.ErrorIs(t, err, ErrEmptyObjectName)
}

func stamps(vs []Version) []uint64 {
	out := make([]uint64, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Stamp)
	}
	return out
}

func namesOf(t *testing.T, rows []row) []string {
	t.Helper()
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		vk, err := ParseVersionKey(r.key)
		require.NoError(t, err)
		out = append(out, vk.Name)
	}
	return out
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// A COPY'S STANDING FOR A VERSION is whether it holds that row, a newer one of the
// name, and whether the row is kept — on every backend the index runs on.
func TestStandingSaysWhatACopyHoldsOfAVersion(t *testing.T) {
	eachBackend(t, func(t *testing.T, store Datastore) {
		ctx := t.Context()
		i := index{store: store}
		a := addr(t)
		r1, r2, r3 := testCID(t, "one").String(), testCID(t, "two").String(), testCID(t, "three").String()
		st := func(stamp uint64, root string) standing {
			t.Helper()
			s, err := i.Standing(ctx, a, "obj", stamp, root)
			require.NoError(t, err)
			return s
		}

		require.Equal(t, standing{}, st(10, r1), "an empty copy holds nothing")
		_, err := i.Append(ctx, a, "obj", 10, "w1", VersionRecord{RootCID: r1})
		require.NoError(t, err)
		require.Equal(t, standing{held: true}, st(10, r1))
		require.Equal(t, standing{}, st(10, r2), "a row at the stamp with other bytes is not the version")
		require.Equal(t, standing{}, st(11, r1))

		_, err = i.Append(ctx, a, "obj", 20, "w1", VersionRecord{RootCID: r2, Kept: true})
		require.NoError(t, err)
		require.Equal(t, standing{held: true, superseded: true}, st(10, r1))
		require.Equal(t, standing{held: true, kept: true}, st(20, r2))

		_, err = i.Append(ctx, a, "obj", 30, "w1", VersionRecord{Tombstone: true})
		require.NoError(t, err)
		require.Equal(t, standing{held: true, superseded: true, kept: true}, st(20, r2), "a tombstone supersedes")

		// Two writers at one stamp: the one byte order puts second is superseded.
		_, err = i.Append(ctx, a, "obj", 40, "w1", VersionRecord{RootCID: r1})
		require.NoError(t, err)
		_, err = i.Append(ctx, a, "obj", 40, "w2", VersionRecord{RootCID: r3})
		require.NoError(t, err)
		vs, err := i.At(ctx, a, "obj", 40)
		require.NoError(t, err)
		require.Len(t, vs, 2)
		require.Equal(t, standing{held: true}, st(40, vs[0].Record.RootCID))
		require.Equal(t, standing{held: true, superseded: true}, st(40, vs[1].Record.RootCID))

		_, err = i.Standing(ctx, a, "", 1, r1)
		require.ErrorIs(t, err, ErrEmptyObjectName)
	})
}
