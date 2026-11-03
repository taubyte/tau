package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/services/worker/storage"
)

// The node's half of an object: bytes in the content-addressed store, the name in a
// replicated row.

// blobs is an Assets double: a content-addressed store over a map.
//
// THE ID IS A FUNCTION OF THE CONTENT, which is the property this package leans
// on twice — two names holding one content are one object, and the id is the
// etag. A double that minted a fresh id per write would hide both.
type blobs struct {
	mu   sync.Mutex
	held map[string][]byte

	putErr error

	// truncate, when set, is a Get that answers a reader which dies partway
	// through — a block the DAG could not finish serving.
	truncate error
}

func newBlobs() *blobs { return &blobs{held: map[string][]byte{}} }

func (b *blobs) Put(_ context.Context, content io.Reader) (string, error) {
	body, err := io.ReadAll(content)
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.putErr != nil {
		return "", b.putErr
	}

	sum := sha256.Sum256(body)
	id := "Qm" + hex.EncodeToString(sum[:])
	b.held[id] = body
	return id, nil
}

func (b *blobs) Get(_ context.Context, id string) (io.ReadSeekCloser, error) {
	b.mu.Lock()
	body, ok := b.held[id]
	dies := b.truncate
	b.mu.Unlock()
	if !ok {
		return nil, os.ErrNotExist
	}
	if dies != nil {
		return dying{err: dies}, nil
	}
	return nopSeekCloser{bytes.NewReader(body)}, nil
}

// dying is a reader that opened and cannot be read to the end.
type dying struct{ err error }

func (d dying) Read([]byte) (int, error)     { return 0, d.err }
func (dying) Seek(int64, int) (int64, error) { return 0, nil }
func (dying) Close() error                   { return nil }

func (b *blobs) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.held)
}

func (b *blobs) holds(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.held[id]
	return ok
}

// lose drops every byte, keeping the names that point at them.
func (b *blobs) lose() {
	b.mu.Lock()
	b.held = map[string][]byte{}
	b.mu.Unlock()
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

// rows is a Rows double over a map that normalises keys as a real store does
// (datastoreKey), or it would hide prefix and `..` bugs.
type rows struct {
	mu   sync.Mutex
	held map[string][]byte

	getErr  error
	putErr  error
	listErr error

	// vanished is listed and not gettable: a row somebody else deleted between
	// a listing and the read of it.
	vanished map[string]bool
}

func newRows() *rows { return &rows{held: map[string][]byte{}, vanished: map[string]bool{}} }

// datastoreKey is what pkg/kvdb does to every key it is handed: it wraps it in a
// datastore path, which makes the key absolute and path-CLEANS it.
func datastoreKey(key string) string {
	cleaned := path.Clean("/" + key)
	if cleaned == "/" {
		return "/"
	}
	// A prefix keeps its trailing separator, which is what makes a prefix scan
	// a scan of one bucket rather than of every sibling that starts the same.
	if strings.HasSuffix(key, "/") {
		return cleaned + "/"
	}
	return cleaned
}

func (r *rows) Get(_ context.Context, key string) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getErr != nil {
		return nil, false, r.getErr
	}
	at := datastoreKey(key)
	if r.vanished[at] {
		return nil, false, nil
	}
	value, held := r.held[at]
	return value, held, nil
}

func (r *rows) Put(_ context.Context, key string, value []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.putErr != nil {
		return r.putErr
	}
	r.held[datastoreKey(key)] = value
	return nil
}

// Delete swallows a key that is not there, which is what the root's adapter
// does: a delete of something already gone is the state the caller asked for.
func (r *rows) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.held, datastoreKey(key))
	return nil
}

// List answers WHOLE KEYS, prefix and all, as a real one does.
func (r *rows) List(_ context.Context, prefix string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listErr != nil {
		return nil, r.listErr
	}

	at := datastoreKey(prefix)
	var out []string
	for key := range r.held {
		if strings.HasPrefix(key, at) {
			out = append(out, key)
		}
	}
	for key := range r.vanished {
		if strings.HasPrefix(key, at) {
			out = append(out, key)
		}
	}
	return out, nil
}

// keys is every row the index holds, for a test that pins the layout.
func (r *rows) keys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.held))
	for key := range r.held {
		out = append(out, key)
	}
	return out
}

// opening is a Stores double, and it counts: the index is opened once on the
// first bucket that needs one, not per open.
type opening struct {
	rows  storage.Rows
	err   error
	mu    sync.Mutex
	asked int
}

func (o *opening) Store() (storage.Rows, error) {
	o.mu.Lock()
	o.asked++
	o.mu.Unlock()
	if o.err != nil {
		return nil, o.err
	}
	return o.rows, nil
}

func (o *opening) times() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.asked
}

// What one row may hold, in S3's own numbers.
//
// THEY ARE SPELLED OUT HERE rather than read off the package: they bound a row
// every peer replicates, so a test that took them from the code under test could
// not notice one being raised.
const (
	maxKeyBytes    = 1024
	maxTypeBytes   = 256
	maxMetaBytes   = 2048
	maxMetaEntries = 32
	maxKeys        = 1000
)

// bucketGrant is what services/worker hands the tier for one `storage:` entry.
func bucketGrant(handle string, size int64) invoke.Grant {
	settings := map[string]string{"type": "object"}
	if size > 0 {
		settings["size"] = strconv.FormatInt(size, 10)
	}
	return invoke.Grant{
		Kind: "storage", Label: "receipts", Handle: handle,
		Ops:      []string{"get", "head", "put", "delete", "list"},
		Settings: settings,
	}
}

// serving is a bucket backing over a fresh store and index.
func serving(t *testing.T) (*storage.Buckets, *blobs, *rows) {
	t.Helper()

	store, index := newBlobs(), newRows()
	b, err := storage.New(store, &opening{rows: index}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })
	return b, store, index
}

// opened is one bucket over a fresh backing.
func opened(t *testing.T, size int64) (wasmtier.Bucket, *blobs, *rows) {
	t.Helper()

	b, store, index := serving(t)
	bucket, err := b.OpenBucket(t.Context(), bucketGrant("shop/prod/QmBucket", size))
	require.NoError(t, err)
	return bucket, store, index
}

func TestEveryDependencyABucketCannotDoWithoutIsRefused(t *testing.T) {
	_, err := storage.New(nil, &opening{rows: newRows()}, nil)
	require.ErrorIs(t, err, storage.ErrNoObjectStore)

	_, err = storage.New(newBlobs(), nil, nil)
	require.ErrorContains(t, err, "no index")

	b, err := storage.New(newBlobs(), &opening{rows: newRows()}, nil)
	require.NoError(t, err)
	require.NoError(t, b.Close(), "closing releases nothing of its own")
}

func TestTheIndexIsAskedForOnce(t *testing.T) {
	stores := &opening{rows: newRows()}
	b, err := storage.New(newBlobs(), stores, nil)
	require.NoError(t, err)
	require.Zero(t, stores.times(), "a backing nobody has asked opened a database")

	for range 3 {
		_, err := b.OpenBucket(t.Context(), bucketGrant("shop/prod/QmBucket", 0))
		require.NoError(t, err)
	}
	require.Equal(t, 1, stores.times(), "the index was opened per bucket")

	refuses, err := storage.New(newBlobs(), &opening{err: errors.New("no database factory")}, nil)
	require.NoError(t, err)
	_, err = refuses.OpenBucket(t.Context(), bucketGrant("shop/prod/QmBucket", 0))
	require.ErrorContains(t, err, "no database factory")
}

func TestAnObjectIsACidAndAName(t *testing.T) {
	bucket, store, index := opened(t, 0)
	ctx := t.Context()

	_, err := bucket.Put(ctx, "2026/01", []byte("a receipt"), "", nil)
	require.NoError(t, err)

	object, err := bucket.Get(ctx, "2026/01")
	require.NoError(t, err)
	require.Equal(t, "a receipt", string(object.Body))

	require.Equal(t, 1, store.count(), "one object, one blob")
	require.Equal(t, []string{"/objects/shop/prod/QmBucket/2026/01"}, index.keys())
}

func TestIdenticalObjectsShareTheirBytes(t *testing.T) {
	bucket, store, _ := opened(t, 0)
	ctx := t.Context()

	for _, name := range []string{"a", "b"} {
		_, err := bucket.Put(ctx, name, []byte("same"), "", nil)
		require.NoError(t, err)
	}
	require.Equal(t, 1, store.count(), "two names, one blob")

	listing, err := bucket.List(ctx, wasmtier.ListRequest{})
	require.NoError(t, err)
	require.Len(t, listing.Objects, 2)
}

func TestDeletingLeavesTheBytes(t *testing.T) {
	bucket, store, _ := opened(t, 0)
	ctx := t.Context()

	for _, name := range []string{"a", "b"} {
		_, err := bucket.Put(ctx, name, []byte("kept"), "", nil)
		require.NoError(t, err)
	}
	require.NoError(t, bucket.Delete(ctx, "a"))

	object, err := bucket.Get(ctx, "b")
	require.NoError(t, err)
	require.Equal(t, "kept", string(object.Body))
	require.Equal(t, 1, store.count())

	_, err = bucket.Get(ctx, "a")
	require.ErrorIs(t, err, wasmtier.ErrNotFound)

	// AND DELETING WHAT IS NOT THERE IS A MISS rather than a success: a caller
	// told its delete worked would stop looking for the name it meant.
	require.ErrorIs(t, bucket.Delete(ctx, "a"), wasmtier.ErrNotFound)
}

func TestBucketsAreScopedByHandle(t *testing.T) {
	b, _, _ := serving(t)
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

func TestANameThatLeavesTheBucketIsRefused(t *testing.T) {
	bucket, _, index := opened(t, 0)
	ctx := t.Context()

	_, err := bucket.Put(ctx, "mine", []byte("mine"), "", nil)
	require.NoError(t, err)

	for _, name := range []string{
		"..", ".", "../QmOther/secret", "../../prod/QmOther/secret",
		"a/../../b", "a/./b", "nested/../../../escape",
	} {
		_, err := bucket.Put(ctx, name, []byte("theirs"), "", nil)
		require.ErrorIs(t, err, storage.ErrBadObjectName, "put %q", name)

		_, err = bucket.Get(ctx, name)
		require.ErrorIs(t, err, storage.ErrBadObjectName, "get %q", name)

		_, err = bucket.Head(ctx, name)
		require.ErrorIs(t, err, storage.ErrBadObjectName, "head %q", name)

		require.ErrorIs(t, bucket.Delete(ctx, name), storage.ErrBadObjectName, "delete %q", name)

		_, err = bucket.List(ctx, wasmtier.ListRequest{Prefix: name})
		require.ErrorIs(t, err, storage.ErrBadObjectName, "list %q", name)
	}

	// AND NOTHING WAS WRITTEN ANYWHERE by any of them.
	require.Equal(t, []string{"/objects/shop/prod/QmBucket/mine"}, index.keys())
}

func TestAnUnnamedObjectIsRefused(t *testing.T) {
	bucket, _, _ := opened(t, 0)

	_, err := bucket.Put(t.Context(), "", []byte("x"), "", nil)
	require.ErrorIs(t, err, storage.ErrBadObjectName)

	// AN EMPTY PREFIX IS NOT AN EMPTY NAME: it is every object, which is what a
	// listing with no prefix asks for.
	listing, err := bucket.List(t.Context(), wasmtier.ListRequest{})
	require.NoError(t, err)
	require.Empty(t, listing.Objects)
}

func TestHeadIsTheRowAlone(t *testing.T) {
	bucket, store, _ := opened(t, 0)
	ctx := t.Context()

	_, err := bucket.Put(ctx, "k", []byte("0123456789"), "", nil)
	require.NoError(t, err)

	info, err := bucket.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "k", info.Key)
	require.EqualValues(t, 10, info.Size)
	require.NotZero(t, info.Modified)
	require.Equal(t, 1, store.count())

	_, err = bucket.Head(ctx, "nobody wrote this")
	require.ErrorIs(t, err, wasmtier.ErrNotFound)
}

func TestTheModifiedTimeIsTheNodesClock(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	b, err := storage.New(newBlobs(), &opening{rows: newRows()}, func() time.Time { return at })
	require.NoError(t, err)

	bucket, err := b.OpenBucket(t.Context(), bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)
	_, err = bucket.Put(t.Context(), "k", []byte("x"), "", nil)
	require.NoError(t, err)

	info, err := bucket.Head(t.Context(), "k")
	require.NoError(t, err)
	require.Equal(t, at.Unix(), info.Modified)

	// AND WITHOUT ONE IT IS THE MACHINE'S, which is every node in production.
	machine, _, _ := opened(t, 0)
	_, err = machine.Put(t.Context(), "k", []byte("x"), "", nil)
	require.NoError(t, err)
	info, err = machine.Head(t.Context(), "k")
	require.NoError(t, err)
	require.InDelta(t, time.Now().Unix(), info.Modified, 60)
}

func TestAnObjectKeepsItsTypeAndMetadata(t *testing.T) {
	bucket, _, _ := opened(t, 0)
	ctx := t.Context()

	_, err := bucket.Put(ctx, "k", []byte("{}"), "application/json", []wasmtier.Metadata{
		{Name: "region", Value: "eu"},
		{Name: "order", Value: "order-1"},
		// A NAME GIVEN TWICE KEEPS THE LAST VALUE, which is what a header map
		// does everywhere — and what keeps the row one value per name.
		{Name: "region", Value: "us"},
	})
	require.NoError(t, err)

	info, err := bucket.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "application/json", info.ContentType)

	// SORTED, so two reads of one object answer the same thing — which is also
	// what keeps the row itself byte-identical between nodes.
	require.Equal(t, []wasmtier.Metadata{
		{Name: "order", Value: "order-1"},
		{Name: "region", Value: "us"},
	}, info.Metadata)

	// AND AN OBJECT THAT CARRIED NONE SAYS NONE rather than an empty list.
	_, err = bucket.Put(ctx, "plain", []byte("x"), "", nil)
	require.NoError(t, err)
	info, err = bucket.Head(ctx, "plain")
	require.NoError(t, err)
	require.Nil(t, info.Metadata)
}

func TestTheEtagIsTheContentId(t *testing.T) {
	bucket, store, _ := opened(t, 0)
	ctx := t.Context()

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
	require.True(t, store.holds(first), "the etag names no blob: %s", first)

	info, err := bucket.Head(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, first, info.ETag)
}

func TestBytesThatHaveNotArrivedAreNotAMiss(t *testing.T) {
	bucket, store, _ := opened(t, 0)
	ctx := t.Context()

	_, err := bucket.Put(ctx, "k", []byte("x"), "", nil)
	require.NoError(t, err)
	store.lose()

	_, err = bucket.Get(ctx, "k")
	require.Error(t, err)
	require.NotErrorIs(t, err, wasmtier.ErrNotFound)
	require.ErrorContains(t, err, "reading")

	// AND HEAD STILL ANSWERS, because that is what it is for: the row is here.
	_, err = bucket.Head(ctx, "k")
	require.NoError(t, err)
}

func TestBytesThatStopHalfwayAreAnError(t *testing.T) {
	bucket, store, _ := opened(t, 0)
	ctx := t.Context()

	_, err := bucket.Put(ctx, "k", []byte("0123456789"), "", nil)
	require.NoError(t, err)

	store.mu.Lock()
	store.truncate = errors.New("the block never arrived")
	store.mu.Unlock()

	_, err = bucket.Get(ctx, "k")
	require.ErrorContains(t, err, "the block never arrived")
	require.NotErrorIs(t, err, wasmtier.ErrNotFound)
}

func TestAnIndexRowThatIsNotOneIsAnError(t *testing.T) {
	bucket, _, index := opened(t, 0)
	ctx := t.Context()

	require.NoError(t, index.Put(ctx, "objects/shop/prod/QmBucket/k", []byte("{not json")))

	_, err := bucket.Get(ctx, "k")
	require.ErrorContains(t, err, "the index row for k is not one")

	_, err = bucket.Head(ctx, "k")
	require.ErrorContains(t, err, "is not one")
}

func TestAnIndexThatCannotBeReadIsNotAMiss(t *testing.T) {
	store, index := newBlobs(), newRows()
	b, err := storage.New(store, &opening{rows: index}, nil)
	require.NoError(t, err)
	bucket, err := b.OpenBucket(t.Context(), bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)

	index.getErr = errors.New("the row could not be read")

	_, err = bucket.Get(t.Context(), "k")
	require.ErrorContains(t, err, "could not be read")
	require.NotErrorIs(t, err, wasmtier.ErrNotFound)

	_, err = bucket.Head(t.Context(), "k")
	require.NotErrorIs(t, err, wasmtier.ErrNotFound)

	require.NotErrorIs(t, bucket.Delete(t.Context(), "k"), wasmtier.ErrNotFound)
}

func TestAWriteThatCannotBeRecordedIsAnError(t *testing.T) {
	store, index := newBlobs(), newRows()
	index.putErr = errors.New("the row was rejected")

	b, err := storage.New(store, &opening{rows: index}, nil)
	require.NoError(t, err)
	bucket, err := b.OpenBucket(t.Context(), bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)

	_, err = bucket.Put(t.Context(), "k", []byte("x"), "", nil)
	require.ErrorContains(t, err, "the row was rejected")
}

func TestBytesThatCannotBeStoredAreAnError(t *testing.T) {
	store, index := newBlobs(), newRows()
	store.putErr = errors.New("the dag is closed")

	b, err := storage.New(store, &opening{rows: index}, nil)
	require.NoError(t, err)
	bucket, err := b.OpenBucket(t.Context(), bucketGrant("shop/prod/QmBucket", 0))
	require.NoError(t, err)

	_, err = bucket.Put(t.Context(), "k", []byte("x"), "", nil)
	require.ErrorContains(t, err, "storing k")
	require.Empty(t, index.keys(), "a name was recorded for bytes nothing holds")
}

func TestABucketRefusesToGrowPastItsSize(t *testing.T) {
	bucket, _, _ := opened(t, 16)
	ctx := t.Context()

	_, err := bucket.Put(ctx, "a", []byte("0123456789"), "", nil)
	require.NoError(t, err)

	_, err = bucket.Put(ctx, "b", []byte("0123456789"), "", nil)
	require.ErrorIs(t, err, wasmtier.ErrFull)

	// AND THE REFUSED WRITE WROTE NOTHING.
	_, err = bucket.Get(ctx, "b")
	require.ErrorIs(t, err, wasmtier.ErrNotFound)
}

func TestOverwritingAnObjectDoesNotFillTheBucket(t *testing.T) {
	bucket, _, _ := opened(t, 16)
	ctx := t.Context()

	_, err := bucket.Put(ctx, "a", []byte("0123456789"), "", nil)
	require.NoError(t, err)
	_, err = bucket.Put(ctx, "a", []byte("9876543210"), "", nil)
	require.NoError(t, err)

	object, err := bucket.Get(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, "9876543210", string(object.Body))
}

func TestABucketWithNoDeclaredSizeIsNotFull(t *testing.T) {
	bucket, _, _ := opened(t, 0)

	for i := range 4 {
		_, err := bucket.Put(t.Context(), strconv.Itoa(i), bytes.Repeat([]byte("x"), 1024), "", nil)
		require.NoError(t, err)
	}
}

func TestACeilingThatCannotBeCountedRefusesTheWrite(t *testing.T) {
	store, index := newBlobs(), newRows()
	b, err := storage.New(store, &opening{rows: index}, nil)
	require.NoError(t, err)
	bucket, err := b.OpenBucket(t.Context(), bucketGrant("shop/prod/QmBucket", 16))
	require.NoError(t, err)

	index.listErr = errors.New("the index could not be scanned")

	_, err = bucket.Put(t.Context(), "k", []byte("x"), "", nil)
	require.ErrorContains(t, err, "could not be scanned")

	// AND A LISTING SAYS SO TOO rather than answering an empty bucket.
	_, err = bucket.List(t.Context(), wasmtier.ListRequest{})
	require.ErrorContains(t, err, "could not be scanned")
}

func TestACorruptRowDoesNotBlockTheBucket(t *testing.T) {
	bucket, _, index := opened(t, 64)
	ctx := t.Context()

	require.NoError(t, index.Put(ctx, "objects/shop/prod/QmBucket/bad", []byte("{not json")))

	_, err := bucket.Put(ctx, "k", []byte("0123456789"), "", nil)
	require.NoError(t, err)
}

func TestAnIndexRowIsBounded(t *testing.T) {
	bucket, _, index := opened(t, 0)
	ctx := t.Context()

	_, err := bucket.Put(ctx, strings.Repeat("k", maxKeyBytes+1), []byte("x"), "", nil)
	require.ErrorIs(t, err, wasmtier.ErrBadRequest, "an unbounded key")

	_, err = bucket.Put(ctx, "k", []byte("x"), strings.Repeat("t", maxTypeBytes+1), nil)
	require.ErrorIs(t, err, wasmtier.ErrBadRequest, "an unbounded content type")

	_, err = bucket.Put(ctx, "k", []byte("x"), "", []wasmtier.Metadata{
		{Name: "big", Value: strings.Repeat("v", maxMetaBytes)},
	})
	require.ErrorIs(t, err, wasmtier.ErrBadRequest, "unbounded metadata")

	many := make([]wasmtier.Metadata, maxMetaEntries+1)
	for i := range many {
		many[i] = wasmtier.Metadata{Name: strconv.Itoa(i), Value: "v"}
	}
	_, err = bucket.Put(ctx, "k", []byte("x"), "", many)
	require.ErrorIs(t, err, wasmtier.ErrBadRequest, "unbounded metadata entries")

	// AND NOTHING WAS WRITTEN by any of them.
	require.Empty(t, index.keys())

	// AND WHAT IS EXACTLY AT THE CEILING IS ACCEPTED, which is what makes it a
	// ceiling rather than a suggestion.
	_, err = bucket.Put(ctx, strings.Repeat("k", maxKeyBytes), []byte("x"),
		strings.Repeat("t", maxTypeBytes), []wasmtier.Metadata{
			{Name: "big", Value: strings.Repeat("v", maxMetaBytes-len("big"))},
		})
	require.NoError(t, err)
}

func TestEntriesAreSortedUnderAPrefix(t *testing.T) {
	bucket, _, _ := opened(t, 0)
	ctx := t.Context()

	for _, name := range []string{"2026/02", "2025/12", "2026/01"} {
		_, err := bucket.Put(ctx, name, []byte(name), "", nil)
		require.NoError(t, err)
	}

	listing, err := bucket.List(ctx, wasmtier.ListRequest{Prefix: "2026"})
	require.NoError(t, err)
	require.Len(t, listing.Objects, 2)
	require.Equal(t, "2026/01", listing.Objects[0].Key)
	require.Equal(t, "2026/02", listing.Objects[1].Key)
}

func TestADelimiterFoldsIntoCommonPrefixes(t *testing.T) {
	bucket, _, _ := opened(t, 0)
	ctx := t.Context()

	for _, name := range []string{"2026/01/a", "2026/01/b", "2026/02/a", "2025/12/a", "top"} {
		_, err := bucket.Put(ctx, name, []byte("x"), "", nil)
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

func TestAListingResumesAfterEverythingItConsumed(t *testing.T) {
	bucket, _, _ := opened(t, 0)
	ctx := t.Context()

	for _, name := range []string{"a/1", "a/2", "a/3", "b/1", "c"} {
		_, err := bucket.Put(ctx, name, []byte("x"), "", nil)
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

func TestAPageCountsPrefixesAsKeys(t *testing.T) {
	bucket, _, _ := opened(t, 0)
	ctx := t.Context()

	for _, name := range []string{"a/1", "b", "c/1", "d"} {
		_, err := bucket.Put(ctx, name, []byte("x"), "", nil)
		require.NoError(t, err)
	}

	page, err := bucket.List(ctx, wasmtier.ListRequest{Delimiter: "/", MaxKeys: 2})
	require.NoError(t, err)
	require.Len(t, page.CommonPrefixes, 1)
	require.Len(t, page.Objects, 1)
	require.True(t, page.Truncated)
}

func TestAnUnissuedContinuationTokenIsRefused(t *testing.T) {
	bucket, _, _ := opened(t, 0)

	_, err := bucket.List(t.Context(), wasmtier.ListRequest{Token: "not base64!!"})
	require.ErrorIs(t, err, wasmtier.ErrBadRequest)
	require.ErrorIs(t, err, storage.ErrBadContinuation)
}

func TestAListingIsCappedAtWhatS3Caps(t *testing.T) {
	bucket, _, _ := opened(t, 0)
	ctx := t.Context()

	for i := range maxKeys + 1 {
		_, err := bucket.Put(ctx, fmt.Sprintf("%06d", i), []byte("x"), "", nil)
		require.NoError(t, err)
	}

	for _, asked := range []uint32{0, maxKeys + 1, 5_000} {
		page, err := bucket.List(ctx, wasmtier.ListRequest{MaxKeys: asked})
		require.NoError(t, err)
		require.Len(t, page.Objects, maxKeys, "asked for %d", asked)
		require.True(t, page.Truncated)
	}

	// AND AN EMPTY BUCKET IS NOT A TRUNCATED PAGE.
	empty, _, _ := opened(t, 0)
	page, err := empty.List(ctx, wasmtier.ListRequest{MaxKeys: 5_000})
	require.NoError(t, err)
	require.False(t, page.Truncated)
}

func TestARowThatVanishedMidListingIsSkippedAndConsumed(t *testing.T) {
	bucket, _, index := opened(t, 0)
	ctx := t.Context()

	for _, name := range []string{"a", "b", "c"} {
		_, err := bucket.Put(ctx, name, []byte("x"), "", nil)
		require.NoError(t, err)
	}

	index.mu.Lock()
	index.vanished["/objects/shop/prod/QmBucket/b"] = true
	index.mu.Unlock()

	listing, err := bucket.List(ctx, wasmtier.ListRequest{})
	require.NoError(t, err)
	require.Len(t, listing.Objects, 2)
	require.Equal(t, "a", listing.Objects[0].Key)
	require.Equal(t, "c", listing.Objects[1].Key)

	// AND A PAGE THAT ENDED ON IT RESUMES PAST IT: the token names the last
	// name looked at, whether it was answered or not.
	page, err := bucket.List(ctx, wasmtier.ListRequest{MaxKeys: 1})
	require.NoError(t, err)
	require.Equal(t, "a", page.Objects[0].Key)
	require.True(t, page.Truncated)

	next, err := bucket.List(ctx, wasmtier.ListRequest{MaxKeys: 1, Token: page.NextToken})
	require.NoError(t, err)
	require.Len(t, next.Objects, 1)
	require.Equal(t, "c", next.Objects[0].Key, "the resume stopped on the row that vanished")
}

func TestAListingDoesNotFetchTheBytes(t *testing.T) {
	bucket, store, _ := opened(t, 0)
	ctx := t.Context()

	for _, name := range []string{"a", "b"} {
		_, err := bucket.Put(ctx, name, []byte(name), "", nil)
		require.NoError(t, err)
	}
	store.lose()

	listing, err := bucket.List(ctx, wasmtier.ListRequest{})
	require.NoError(t, err, "a listing waited on the DAG")
	require.Len(t, listing.Objects, 2)
	require.NotEmpty(t, listing.Objects[0].ETag)
}

func TestTheRowIsByteIdenticalWhateverOrderTheMetadataCameIn(t *testing.T) {
	ctx := t.Context()
	at := time.Unix(1_700_000_000, 0)

	row := func(metadata []wasmtier.Metadata) []byte {
		index := newRows()
		b, err := storage.New(newBlobs(), &opening{rows: index}, func() time.Time { return at })
		require.NoError(t, err)
		bucket, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmBucket", 0))
		require.NoError(t, err)
		_, err = bucket.Put(ctx, "k", []byte("x"), "text/plain", metadata)
		require.NoError(t, err)

		value, found, err := index.Get(ctx, "objects/shop/prod/QmBucket/k")
		require.NoError(t, err)
		require.True(t, found)
		return value
	}

	one := row([]wasmtier.Metadata{{Name: "region", Value: "eu"}, {Name: "order", Value: "1"}})
	other := row([]wasmtier.Metadata{{Name: "order", Value: "1"}, {Name: "region", Value: "eu"}})
	require.Equal(t, string(one), string(other))

	// AND IT IS JSON A SECOND READER CAN READ, not a Go-specific encoding: the
	// row crosses nodes.
	var held map[string]any
	require.NoError(t, json.Unmarshal(one, &held))
	require.Equal(t, "text/plain", held["content_type"])
}
