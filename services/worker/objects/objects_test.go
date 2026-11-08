package objects_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/services/cm"
	cmmock "github.com/taubyte/tau/services/cm/mock"
	"github.com/taubyte/tau/services/cm/settings"
	"github.com/taubyte/tau/services/storage"
	"github.com/taubyte/tau/services/worker/objects"
)

// node is the instance a storage service runs on for these tests: a peer, cm's mocked
// version answering the region's settings, discovery finding no other storage node.
type node struct {
	peer.Node
	root    string
	mu      sync.Mutex
	running []service.Running
}

func (n *node) Peer() peer.Node { return n.Node }
func (n *node) Running() []service.Running {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.running)
}
func (*node) Config(any) error { return nil }
func (n *node) Root() string   { return n.root }
func (*node) Cloud() string    { return "objects-test" }
func (*node) Region() string   { return "" }
func (*node) Now() time.Time   { return time.Now() }

// served is a storage service on a fresh peer, with a failure each call can be made
// to answer.
type served struct {
	storage.Store

	mu                                sync.Mutex
	putErr, readErr, listErr, current error
}

func (s *served) fail(put, read, list, current error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putErr, s.readErr, s.listErr, s.current = put, read, list, current
}

func (s *served) failing() (put, read, list, current error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putErr, s.readErr, s.listErr, s.current
}

func (s *served) Put(ctx context.Context, at storage.Address, name string, o storage.Object, content io.Reader) (storage.Version, error) {
	if err, _, _, _ := s.failing(); err != nil {
		return storage.Version{}, err
	}
	return s.Store.Put(ctx, at, name, o, content)
}

func (s *served) Read(ctx context.Context, root string) (io.ReadCloser, error) {
	if _, err, _, _ := s.failing(); err != nil {
		return nil, err
	}
	return s.Store.Read(ctx, root)
}

func (s *served) List(ctx context.Context, at storage.Address, prefix, after string, limit int) ([]storage.Version, error) {
	if _, _, err, _ := s.failing(); err != nil {
		return nil, err
	}
	return s.Store.List(ctx, at, prefix, after, limit)
}

func (s *served) Current(ctx context.Context, at storage.Address, name string) (storage.Version, error) {
	if _, _, _, err := s.failing(); err != nil {
		return storage.Version{}, err
	}
	return s.Store.Current(ctx, at, name)
}

func serving(t *testing.T) *served {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	n := &node{Node: p, root: t.TempDir(), running: []service.Running{
		{Name: cm.Name, Service: cmmock.New(settings.Defaults())},
	}}
	s := storage.New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), n))
	return &served{Store: s}
}

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

func opened(t *testing.T, size int64) (wasmtier.Bucket, *served) {
	t.Helper()
	store := serving(t)
	b, err := objects.New(store)
	require.NoError(t, err)
	bucket, err := b.OpenBucket(t.Context(), bucketGrant("shop/prod/QmBucket", size))
	require.NoError(t, err)
	return bucket, store
}

func TestABucketNeedsAStorageService(t *testing.T) {
	_, err := objects.New(nil)
	require.Error(t, err)

	b, err := objects.New(serving(t))
	require.NoError(t, err)
	_, err = b.OpenBucket(t.Context(), bucketGrant("not-a-handle", 0))
	require.Error(t, err, "a handle that names no bucket")
}

func TestAnObjectIsItsBytesAndItsEtagIsItsContent(t *testing.T) {
	ctx := t.Context()
	bucket, _ := opened(t, 0)

	etag, err := bucket.Put(ctx, "receipts/1", []byte("paid"), "text/plain", []wasmtier.Metadata{{Name: "b", Value: "2"}, {Name: "a", Value: "1"}, {Name: "a", Value: "last"}})
	require.NoError(t, err)
	again, err := bucket.Put(ctx, "receipts/2", []byte("paid"), "", nil)
	require.NoError(t, err)
	require.Equal(t, etag, again, "the same content is the same etag")

	got, err := bucket.Get(ctx, "receipts/1")
	require.NoError(t, err)
	require.Equal(t, "paid", string(got.Body))
	require.Equal(t, etag, got.Info.ETag)
	require.Equal(t, int64(4), got.Info.Size)
	require.Equal(t, "text/plain", got.Info.ContentType)
	require.Equal(t, []wasmtier.Metadata{{Name: "a", Value: "last"}, {Name: "b", Value: "2"}}, got.Info.Metadata, "sorted, and a name given twice keeps the last")
	require.NotZero(t, got.Info.Modified)

	head, err := bucket.Head(ctx, "receipts/1")
	require.NoError(t, err)
	require.Equal(t, got.Info, head)

	_, err = bucket.Head(ctx, "absent")
	require.ErrorIs(t, err, wasmtier.ErrNotFound)
	_, err = bucket.Get(ctx, "absent")
	require.ErrorIs(t, err, wasmtier.ErrNotFound)
}

func TestADeletedObjectIsGoneAndAMissingOneIsAMiss(t *testing.T) {
	ctx := t.Context()
	bucket, _ := opened(t, 0)
	_, err := bucket.Put(ctx, "k", []byte("v"), "", nil)
	require.NoError(t, err)

	require.NoError(t, bucket.Delete(ctx, "k"))
	_, err = bucket.Get(ctx, "k")
	require.ErrorIs(t, err, wasmtier.ErrNotFound)
	require.ErrorIs(t, bucket.Delete(ctx, "k"), wasmtier.ErrNotFound)
}

func TestBucketsAreScopedByHandle(t *testing.T) {
	ctx := t.Context()
	store := serving(t)
	b, err := objects.New(store)
	require.NoError(t, err)
	one, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmOne", 0))
	require.NoError(t, err)
	other, err := b.OpenBucket(ctx, bucketGrant("shop/prod/QmOther", 0))
	require.NoError(t, err)

	_, err = one.Put(ctx, "k", []byte("one"), "", nil)
	require.NoError(t, err)
	_, err = other.Get(ctx, "k")
	require.ErrorIs(t, err, wasmtier.ErrNotFound)
}

func TestWhatWouldNotFitInARowIsRefused(t *testing.T) {
	ctx := t.Context()
	bucket, _ := opened(t, 0)

	_, err := bucket.Put(ctx, "", []byte("v"), "", nil)
	require.ErrorIs(t, err, objects.ErrNoName)
	require.ErrorIs(t, err, wasmtier.ErrBadRequest)

	many := make([]wasmtier.Metadata, 33)
	for i := range many {
		many[i] = wasmtier.Metadata{Name: fmt.Sprint("m", i), Value: "v"}
	}
	for name, put := range map[string]func() error{
		"a key over 1024 bytes":         func() error { _, err := bucket.Put(ctx, strings.Repeat("k", 1025), nil, "", nil); return err },
		"a content type over 256 bytes": func() error { _, err := bucket.Put(ctx, "k", nil, strings.Repeat("t", 257), nil); return err },
		"more than 32 metadata entries": func() error { _, err := bucket.Put(ctx, "k", nil, "", many); return err },
		"metadata over 2048 bytes": func() error {
			_, err := bucket.Put(ctx, "k", nil, "", []wasmtier.Metadata{{Name: "m", Value: strings.Repeat("v", 2048)}})
			return err
		},
	} {
		require.ErrorIs(t, put(), wasmtier.ErrBadRequest, name)
	}
}

func TestABucketRefusesToGrowPastItsSize(t *testing.T) {
	ctx := t.Context()
	bucket, _ := opened(t, 10)

	_, err := bucket.Put(ctx, "a", []byte("123456"), "", nil)
	require.NoError(t, err)
	_, err = bucket.Put(ctx, "b", []byte("123456"), "", nil)
	require.ErrorIs(t, err, wasmtier.ErrFull)

	_, err = bucket.Put(ctx, "a", []byte("1234567890"), "", nil)
	require.NoError(t, err, "replacing an object takes its old size off")

	unbounded, _ := opened(t, 0)
	_, err = unbounded.Put(ctx, "big", make([]byte, 1<<16), "", nil)
	require.NoError(t, err, "no declared size is no ceiling")
}

func TestAListingIsS3s(t *testing.T) {
	ctx := t.Context()
	bucket, _ := opened(t, 0)
	for _, key := range []string{"a/1", "a/2", "b/1", "c", "d"} {
		_, err := bucket.Put(ctx, key, []byte(key), "", nil)
		require.NoError(t, err)
	}

	all, err := bucket.List(ctx, wasmtier.ListRequest{})
	require.NoError(t, err)
	require.Len(t, all.Objects, 5)
	require.Equal(t, "a/1", all.Objects[0].Key, "in name order")

	under, err := bucket.List(ctx, wasmtier.ListRequest{Prefix: "a/"})
	require.NoError(t, err)
	require.Len(t, under.Objects, 2)

	folded, err := bucket.List(ctx, wasmtier.ListRequest{Delimiter: "/"})
	require.NoError(t, err)
	require.Equal(t, []string{"a/", "b/"}, folded.CommonPrefixes)
	require.Len(t, folded.Objects, 2)

	// A PAGE COUNTS PREFIXES AS KEYS, and resumes past every member of one.
	page, err := bucket.List(ctx, wasmtier.ListRequest{Delimiter: "/", MaxKeys: 1})
	require.NoError(t, err)
	require.True(t, page.Truncated)
	require.Equal(t, []string{"a/"}, page.CommonPrefixes)
	next, err := bucket.List(ctx, wasmtier.ListRequest{Delimiter: "/", MaxKeys: 1, Token: page.NextToken})
	require.NoError(t, err)
	require.Equal(t, []string{"b/"}, next.CommonPrefixes)

	capped, err := bucket.List(ctx, wasmtier.ListRequest{MaxKeys: 5000})
	require.NoError(t, err)
	require.Len(t, capped.Objects, 5, "an ask over S3's 1000 is S3's 1000")

	_, err = bucket.List(ctx, wasmtier.ListRequest{Token: "!!! not a token"})
	require.ErrorIs(t, err, objects.ErrBadContinuation)
}

func TestAStoreThatFailsIsNotAMiss(t *testing.T) {
	ctx := t.Context()
	bucket, store := opened(t, 10)
	_, err := bucket.Put(ctx, "k", []byte("v"), "", nil)
	require.NoError(t, err)
	down := errors.New("the storage service is down")

	store.fail(nil, down, nil, nil)
	_, err = bucket.Get(ctx, "k")
	require.ErrorIs(t, err, down, "the name is here and the bytes are not")
	require.NotErrorIs(t, err, wasmtier.ErrNotFound)

	store.fail(nil, nil, nil, down)
	_, err = bucket.Head(ctx, "k")
	require.ErrorIs(t, err, down)
	require.ErrorIs(t, bucket.Delete(ctx, "k"), down)

	store.fail(nil, nil, down, nil)
	_, err = bucket.List(ctx, wasmtier.ListRequest{})
	require.ErrorIs(t, err, down)
	_, err = bucket.Put(ctx, "k2", []byte("v"), "", nil)
	require.ErrorIs(t, err, down, "a ceiling that cannot be counted refuses the write")

	store.fail(down, nil, nil, nil)
	_, err = bucket.Put(ctx, "k2", []byte("v"), "", nil)
	require.ErrorIs(t, err, down)
}
