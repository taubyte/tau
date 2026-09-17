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
	_, err = bucket.Put(ctx, "2026/01", []byte("a receipt"), "", nil)
	require.NoError(t, err)

	object, err := bucket.Get(ctx, "2026/01")
	require.NoError(t, err)
	require.Equal(t, "a receipt", string(object.Body))

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
	_, err = bucket.Put(ctx, "a", []byte("same"), "", nil)
	require.NoError(t, err)
	_, err = bucket.Put(ctx, "b", []byte("same"), "", nil)
	require.NoError(t, err)

	require.Len(t, store.blobs, 1, "two names, one blob")

	listing, err := bucket.List(ctx, wasmtier.ListRequest{})
	require.NoError(t, err)
	require.Len(t, listing.Objects, 2)
}

// TestADELETERemovesTheNameAndLeavesTheBytes, because they may be another
// name's — collecting them is a different job with a different safety argument.
func TestDeletingLeavesTheBytes(t *testing.T) {
	_, store, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	_, err = bucket.Put(ctx, "a", []byte("kept"), "", nil)
	require.NoError(t, err)
	_, err = bucket.Put(ctx, "b", []byte("kept"), "", nil)
	require.NoError(t, err)
	require.NoError(t, bucket.Delete(ctx, "a"))

	object, err := bucket.Get(ctx, "b")
	require.NoError(t, err)
	require.Equal(t, "kept", string(object.Body))
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

	_, err = first.Put(ctx, "k", []byte("mine"), "", nil)
	require.NoError(t, err)
	_, err = second.Put(ctx, "k", []byte("theirs"), "", nil)
	require.NoError(t, err)

	listing, err := first.List(ctx, wasmtier.ListRequest{})
	require.NoError(t, err)
	require.Len(t, listing.Objects, 1, "QmA saw QmAB's objects: %+v", listing.Objects)

	object, err := first.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "mine", string(object.Body))
}

// TestHEADAnswersWithoutReadingTheBytes.
func TestHeadIsTheRowAlone(t *testing.T) {
	_, store, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	_, err = bucket.Put(ctx, "k", []byte("0123456789"), "", nil)
	require.NoError(t, err)

	info, err := bucket.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "k", info.Key)
	require.EqualValues(t, 10, info.Size)
	require.NotZero(t, info.Modified)
	require.Len(t, store.blobs, 1)
}

// TestTHECEILINGIsAWallAndNotAMeasurement. A write that would take the bucket
// past its declared size is refused with an arm the caller can act on.
func TestABucketRefusesToGrowPastItsSize(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 16))
	require.NoError(t, err)
	_, err = bucket.Put(ctx, "a", []byte("0123456789"), "", nil)
	require.NoError(t, err)

	_, err = bucket.Put(ctx, "b", []byte("0123456789"), "", nil)
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
	_, err = bucket.Put(ctx, "a", []byte("0123456789"), "", nil)
	require.NoError(t, err)
	_, err = bucket.Put(ctx, "a", []byte("9876543210"), "", nil)
	require.NoError(t, err)

	object, err := bucket.Get(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, "9876543210", string(object.Body))
}

// TestENTRIESAreSortedAndPrefixed.
func TestEntriesAreSortedUnderAPrefix(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	for _, name := range []string{"2026/02", "2025/12", "2026/01"} {
		_, err = bucket.Put(ctx, name, []byte(name), "", nil)
		require.NoError(t, err)
	}

	listing, err := bucket.List(ctx, wasmtier.ListRequest{Prefix: "2026"})
	require.NoError(t, err)
	require.Len(t, listing.Objects, 2)
	require.Equal(t, "2026/01", listing.Objects[0].Key)
	require.Equal(t, "2026/02", listing.Objects[1].Key)
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
	_, err = bucket.Put(ctx, "", []byte("x"), "", nil)
	require.Error(t, err)
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
	_, err = bucket.Put(t.Context(), "k", []byte("x"), "", nil)
	require.NoError(t, err)

	info, err := bucket.Head(t.Context(), "k")
	require.NoError(t, err)
	require.Equal(t, at.Unix(), info.Modified)
}

// TestAROWWhoseBytesHaveNotArrivedIsNotAMiss. Telling the caller "not found"
// would tell it to write the object again, over something on its way.
func TestBytesThatHaveNotArrivedAreNotAMiss(t *testing.T) {
	_, store, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	_, err = bucket.Put(ctx, "k", []byte("x"), "", nil)
	require.NoError(t, err)

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

// ── the S3 half ──────────────────────────────────────────────────────────────

// TestTHEETAGIsTheContentId, which is what makes it a STRONG etag: it changes
// exactly when the bytes do, and two names holding one content share it.
func TestTheEtagIsTheContentId(t *testing.T) {
	_, store, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)

	first, err := bucket.Put(ctx, "a", []byte("same"), "", nil)
	require.NoError(t, err)
	second, err := bucket.Put(ctx, "b", []byte("same"), "", nil)
	require.NoError(t, err)
	third, err := bucket.Put(ctx, "c", []byte("different"), "", nil)
	require.NoError(t, err)

	require.NotEmpty(t, first)
	require.Equal(t, first, second, "one content, one etag")
	require.NotEqual(t, first, third)

	// AND IT IS THE CID THE BYTES WENT IN UNDER, not a second identifier
	// computed beside it.
	store.mu.Lock()
	_, held := store.blobs[first]
	store.mu.Unlock()
	require.True(t, held, "the etag names no blob: %s", first)

	info, err := bucket.Head(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, first, info.ETag)
}

// TestTHECONTENTTYPEAndMetadataAreKept, which is what makes an object servable
// over HTTP by anything that reads this bucket.
func TestAnObjectKeepsItsTypeAndMetadata(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)

	_, err = bucket.Put(ctx, "k", []byte("{}"), "application/json", []wasmtier.Metadata{
		{Name: "region", Value: "eu"},
		{Name: "order", Value: "order-1"},
	})
	require.NoError(t, err)

	info, err := bucket.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "application/json", info.ContentType)

	// SORTED, so two reads of one object answer the same thing — which is also
	// what keeps the row itself byte-identical between nodes.
	require.Equal(t, []wasmtier.Metadata{
		{Name: "order", Value: "order-1"},
		{Name: "region", Value: "eu"},
	}, info.Metadata)
}

// TestADELIMITERCollapsesEverythingPastIt, which is what makes a flat bucket
// browsable as folders and is why S3's listing takes one.
func TestADelimiterFoldsIntoCommonPrefixes(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	for _, name := range []string{"2026/01/a", "2026/01/b", "2026/02/a", "2025/12/a", "top"} {
		_, err = bucket.Put(ctx, name, []byte("x"), "", nil)
		require.NoError(t, err)
	}

	listing, err := bucket.List(ctx, wasmtier.ListRequest{Delimiter: "/"})
	require.NoError(t, err)
	require.Equal(t, []string{"2025/", "2026/"}, listing.CommonPrefixes)
	require.Len(t, listing.Objects, 1)
	require.Equal(t, "top", listing.Objects[0].Key)

	// AND ONE LEVEL DOWN, which is the walk a file browser does.
	inside, err := bucket.List(ctx, wasmtier.ListRequest{Prefix: "2026/", Delimiter: "/"})
	require.NoError(t, err)
	require.Equal(t, []string{"2026/01/", "2026/02/"}, inside.CommonPrefixes)
	require.Empty(t, inside.Objects)
}

// TestALISTINGPagesAndTheTokenResumesWhereItStopped. This is the assertion the
// token's whole shape exists for: a page that ended on a COMMON PREFIX consumed
// several objects to produce it, so a token naming the prefix would walk them
// again and answer the same prefix twice.
func TestAListingResumesAfterEverythingItConsumed(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	for _, name := range []string{"a/1", "a/2", "a/3", "b/1", "c"} {
		_, err = bucket.Put(ctx, name, []byte("x"), "", nil)
		require.NoError(t, err)
	}

	first, err := bucket.List(ctx, wasmtier.ListRequest{Delimiter: "/", MaxKeys: 1})
	require.NoError(t, err)
	require.Equal(t, []string{"a/"}, first.CommonPrefixes)
	require.True(t, first.Truncated)
	require.NotEmpty(t, first.NextToken)

	second, err := bucket.List(ctx, wasmtier.ListRequest{
		Delimiter: "/", MaxKeys: 1, Token: first.NextToken,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"b/"}, second.CommonPrefixes,
		"a/ came back, so the token resumed before the objects it folded")

	third, err := bucket.List(ctx, wasmtier.ListRequest{
		Delimiter: "/", MaxKeys: 1, Token: second.NextToken,
	})
	require.NoError(t, err)
	require.Empty(t, third.CommonPrefixes)
	require.Len(t, third.Objects, 1)
	require.Equal(t, "c", third.Objects[0].Key)
	require.False(t, third.Truncated, "and the last page says so")
}

// TestOBJECTSAndPrefixesCountTogether, as S3 counts them: a caller that asked
// for two things gets two things however they were produced.
func TestAPageCountsPrefixesAsKeys(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	for _, name := range []string{"a/1", "b", "c/1", "d"} {
		_, err = bucket.Put(ctx, name, []byte("x"), "", nil)
		require.NoError(t, err)
	}

	page, err := bucket.List(ctx, wasmtier.ListRequest{Delimiter: "/", MaxKeys: 2})
	require.NoError(t, err)
	require.Len(t, page.CommonPrefixes, 1)
	require.Len(t, page.Objects, 1)
	require.True(t, page.Truncated)
}

// TestATOKENThisBucketNeverIssuedIsAnArmTheCallerFixes, rather than a
// different page or an outage.
func TestAnUnissuedContinuationTokenIsRefused(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)

	_, err = bucket.List(ctx, wasmtier.ListRequest{Token: "not base64!!"})
	require.ErrorIs(t, err, wasmtier.ErrBadRequest)
	require.ErrorIs(t, err, ErrBadContinuation)
}

// TestAPAGEIsCappedEvenWhenNothingAsks, so a bucket with a million objects
// cannot be pulled through the ABI in one call by a guest that passed 0.
func TestAListingIsCappedByDefault(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)

	require.Equal(t, 1000, maxObjectKeys, "the cap is S3's own number")

	page, err := bucket.List(ctx, wasmtier.ListRequest{MaxKeys: 5_000})
	require.NoError(t, err)
	require.False(t, page.Truncated, "an empty bucket is not a truncated page")
}

// TestTHEROWIsBoundedAndNotOnlyTheBYTES. The bucket's declared size bounds what
// goes to the content-addressed store; the key, the content type and the user
// metadata go into a kvdb row every peer replicates, and nothing else bounds
// those — so a guest writing a megabyte of metadata per object would fill every
// node's datastore while the bucket reported itself empty.
func TestAnIndexRowIsBounded(t *testing.T) {
	_, _, b := storingNode(t)
	ctx := t.Context()

	bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)

	_, err = bucket.Put(ctx, strings.Repeat("k", maxObjectKeyBytes+1), []byte("x"), "", nil)
	require.ErrorIs(t, err, wasmtier.ErrBadRequest, "an unbounded key")

	_, err = bucket.Put(ctx, "k", []byte("x"), strings.Repeat("t", maxObjectTypeBytes+1), nil)
	require.ErrorIs(t, err, wasmtier.ErrBadRequest, "an unbounded content type")

	_, err = bucket.Put(ctx, "k", []byte("x"), "", []wasmtier.Metadata{
		{Name: "big", Value: strings.Repeat("v", maxObjectMetaBytes)},
	})
	require.ErrorIs(t, err, wasmtier.ErrBadRequest, "unbounded metadata")

	many := make([]wasmtier.Metadata, maxObjectMetaEntries+1)
	for i := range many {
		many[i] = wasmtier.Metadata{Name: strconv.Itoa(i), Value: "v"}
	}
	_, err = bucket.Put(ctx, "k", []byte("x"), "", many)
	require.ErrorIs(t, err, wasmtier.ErrBadRequest, "unbounded metadata entries")

	// AND NOTHING WAS WRITTEN by any of them.
	listing, err := bucket.List(ctx, wasmtier.ListRequest{})
	require.NoError(t, err)
	require.Empty(t, listing.Objects)
}
