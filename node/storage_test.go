package node

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// storage_test is the node's half: the bytes go into the content-addressed
// store and the NAME goes into a replicated row, which is what makes two
// objects with the same content one object and still two names.

func bucketGrant(handle string, size int64) invoke.Grant {
	settings := map[string]string{settingObjectType: "object"}
	if size > 0 {
		settings[settingObjectSize] = strconv.FormatInt(size, 10)
	}
	return invoke.Grant{
		Kind: "storage", Label: "receipts", Handle: handle,
		Ops:      []string{"get", "head", "put", "delete", "list"},
		Settings: settings,
	}
}

// storing is a node with an asset store, and the bucket backing over it.
func storingNode(t *testing.T) (*Node, *memAssets, *buckets) {
	t.Helper()

	store := &memAssets{blobs: map[string][]byte{}}
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}}, WithAssets(store))
	return n, store, &buckets{node: n}
}

// TestTHEBYTESGoIntoTheContentAddressedStoreAndTheNameIntoARow.
func TestAnObjectIsACidAndAName(t *testing.T) {
	n, store, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	require.NoError(t, bucket.Put(ctx, "2026/01", []byte("a receipt")))

	value, err := bucket.Get(ctx, "2026/01")
	require.NoError(t, err)
	require.Equal(t, "a receipt", string(value))

	require.Len(t, store.blobs, 1, "one object, one blob")
	_ = n
}

// TestTWOOBJECTSWithTheSameContentAreONEBlob, which is what content-addressing
// buys and is the reason a delete removes the name and not the bytes.
func TestIdenticalObjectsShareTheirBytes(t *testing.T) {
	_, store, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	require.NoError(t, bucket.Put(ctx, "a", []byte("same")))
	require.NoError(t, bucket.Put(ctx, "b", []byte("same")))

	require.Len(t, store.blobs, 1, "two names, one blob")

	entries, err := bucket.Entries(ctx, "")
	require.NoError(t, err)
	require.Len(t, entries, 2)
}

// TestADELETERemovesTheNameAndLeavesTheBytes, because they may be another
// name's — collecting them is a different job with a different safety argument.
func TestDeletingLeavesTheBytes(t *testing.T) {
	_, store, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	require.NoError(t, bucket.Put(ctx, "a", []byte("kept")))
	require.NoError(t, bucket.Put(ctx, "b", []byte("kept")))
	require.NoError(t, bucket.Delete(ctx, "a"))

	value, err := bucket.Get(ctx, "b")
	require.NoError(t, err)
	require.Equal(t, "kept", string(value))
	require.Len(t, store.blobs, 1)

	_, err = bucket.Get(ctx, "a")
	require.ErrorIs(t, err, wasmtier.ErrNotFound)
}

// TestTWOBucketsDoNotSeeEachOther, and a name that is a PREFIX of another
// bucket's handle is the case that catches a missing separator.
func TestBucketsAreScopedByHandle(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	first, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmA", 0))
	require.NoError(t, err)
	second, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmAB", 0))
	require.NoError(t, err)

	require.NoError(t, first.Put(ctx, "k", []byte("mine")))
	require.NoError(t, second.Put(ctx, "k", []byte("theirs")))

	entries, err := first.Entries(ctx, "")
	require.NoError(t, err)
	require.Len(t, entries, 1, "QmA saw QmAB's objects: %+v", entries)

	value, err := first.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "mine", string(value))
}

// TestHEADAnswersWithoutReadingTheBytes.
func TestHeadIsTheRowAlone(t *testing.T) {
	_, store, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	require.NoError(t, bucket.Put(ctx, "k", []byte("0123456789")))

	object, err := bucket.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "k", object.Name)
	require.EqualValues(t, 10, object.Size)
	require.NotZero(t, object.Modified)
	require.Len(t, store.blobs, 1)
}

// TestTHECEILINGIsAWallAndNotAMeasurement. A write that would take the bucket
// past its declared size is refused with an arm the caller can act on.
func TestABucketRefusesToGrowPastItsSize(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 16))
	require.NoError(t, err)
	require.NoError(t, bucket.Put(ctx, "a", []byte("0123456789")))

	err = bucket.Put(ctx, "b", []byte("0123456789"))
	require.ErrorIs(t, err, wasmtier.ErrFull)

	// AND THE REFUSED WRITE WROTE NOTHING.
	_, err = bucket.Get(ctx, "b")
	require.ErrorIs(t, err, wasmtier.ErrNotFound)
}

// TestREPLACINGAnObjectDoesNotCountTwice, because replacing a 10-byte object
// with a 10-byte one does not grow anything.
func TestOverwritingAnObjectDoesNotFillTheBucket(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 16))
	require.NoError(t, err)
	require.NoError(t, bucket.Put(ctx, "a", []byte("0123456789")))
	require.NoError(t, bucket.Put(ctx, "a", []byte("9876543210")))

	value, err := bucket.Get(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, "9876543210", string(value))
}

// TestENTRIESAreSortedAndPrefixed.
func TestEntriesAreSortedUnderAPrefix(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	for _, name := range []string{"2026/02", "2025/12", "2026/01"} {
		require.NoError(t, bucket.Put(ctx, name, []byte(name)))
	}

	entries, err := bucket.Entries(ctx, "2026")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, "2026/01", entries[0].Name)
	require.Equal(t, "2026/02", entries[1].Name)
}

// TestANODEWithNoAssetStoreSaysSO, rather than answering every read with a
// miss.
func TestStorageNeedsAnAssetStore(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})

	_, err := (&buckets{node: n}).OpenBucket(t.Context(), bucketGrant("shop/prod/QmBucket", 0))
	require.ErrorIs(t, err, ErrNoObjectStore)
}

// TestANOBJECTWithNoNameCannotBeFoundAgain.
func TestAnUnnamedObjectIsRefused(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	require.Error(t, bucket.Put(ctx, "", []byte("x")))
}

// TestTHEROWCarriesTheMomentItWasWritten, off this node's own clock so a test
// that moves time moves what an object says.
func TestTheModifiedTimeIsTheNodesClock(t *testing.T) {
	store := &memAssets{blobs: map[string][]byte{}}
	at := time.Unix(1_700_000_000, 0)
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithAssets(store), WithClock(func() time.Time { return at }))

	bucket, err := (&buckets{node: n}).OpenBucket(t.Context(), bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	require.NoError(t, bucket.Put(t.Context(), "k", []byte("x")))

	object, err := bucket.Head(t.Context(), "k")
	require.NoError(t, err)
	require.Equal(t, at.Unix(), object.Modified)
}

// TestAROWWhoseBytesHaveNotArrivedIsNotAMiss. Telling the caller "not found"
// would tell it to write the object again, over something on its way.
func TestBytesThatHaveNotArrivedAreNotAMiss(t *testing.T) {
	_, store, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	require.NoError(t, bucket.Put(ctx, "k", []byte("x")))

	store.mu.Lock()
	for id := range store.blobs {
		delete(store.blobs, id)
	}
	store.mu.Unlock()

	_, err = bucket.Get(ctx, "k")
	require.Error(t, err)
	require.NotErrorIs(t, err, wasmtier.ErrNotFound)
	require.True(t, strings.Contains(err.Error(), "reading"), "got %v", err)
}
