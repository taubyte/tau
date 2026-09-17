package wasm_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// storage_test drives the object store through a REAL COMPONENT, which is the
// only thing that shows the records this interface carries survive both
// directions of the ABI. It is the deepest shape in the tree: `get` answers a
// record CONTAINING a record containing a list of records, and `list` takes one
// record and answers another — and a shape that lifts wrong is a field read off
// the wrong offset rather than an error.

// storageGrant is one bucket a workload may reach.
func storageGrant(label, handle string, ops ...string) invoke.Grant {
	return invoke.Grant{Kind: "storage", Label: label, Handle: handle, Ops: ops}
}

// buckets is a Buckets over maps.
type buckets struct {
	mu     sync.Mutex
	byName map[string]*memBucket
	opened []string
	err    error
}

func newBuckets(handles ...string) *buckets {
	b := &buckets{byName: map[string]*memBucket{}}
	for _, h := range handles {
		b.byName[h] = &memBucket{objects: map[string]storedObject{}}
	}
	return b
}

func (b *buckets) OpenBucket(_ context.Context, grant invoke.Grant) (wasm.Bucket, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.opened = append(b.opened, grant.Handle)
	if b.err != nil {
		return nil, b.err
	}
	bucket, ok := b.byName[grant.Handle]
	if !ok {
		return nil, errors.New("no such handle: " + grant.Handle)
	}
	return bucket, nil
}

type storedObject struct {
	body        []byte
	modified    int64
	contentType string
	metadata    []wasm.Metadata
}

type memBucket struct {
	mu      sync.Mutex
	objects map[string]storedObject

	// full, when set, is the size this bucket refuses to grow past.
	full int64

	// asked is the last listing this bucket was given, so a test can show the
	// whole request record crossed rather than only the part the answer
	// happens to depend on.
	asked wasm.ListRequest
}

// etagOf is the fake's content id: a hash of the bytes, so two objects with the
// same content carry the same etag — which is the property the real store gets
// from being content-addressed and the one a random string would not show.
func etagOf(body []byte) string {
	sum := sha256.Sum256(body)
	return "bafy" + hex.EncodeToString(sum[:8])
}

func (m *memBucket) info(key string, object storedObject) wasm.Info {
	return wasm.Info{
		Key:         key,
		ETag:        etagOf(object.body),
		Size:        int64(len(object.body)),
		Modified:    object.modified,
		ContentType: object.contentType,
		Metadata:    object.metadata,
	}
}

func (m *memBucket) Get(_ context.Context, key string) (wasm.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	object, ok := m.objects[key]
	if !ok {
		return wasm.Object{}, wasm.ErrNotFound
	}
	return wasm.Object{Info: m.info(key, object), Body: object.body}, nil
}

func (m *memBucket) Head(_ context.Context, key string) (wasm.Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	object, ok := m.objects[key]
	if !ok {
		return wasm.Info{}, wasm.ErrNotFound
	}
	return m.info(key, object), nil
}

func (m *memBucket) Put(_ context.Context, key string, value []byte, contentType string, metadata []wasm.Metadata) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.full > 0 && int64(len(value)) > m.full {
		return "", fmt.Errorf("%w: %d bytes over", wasm.ErrFull, int64(len(value))-m.full)
	}
	m.objects[key] = storedObject{
		body:        value,
		modified:    1_700_000_000,
		contentType: contentType,
		metadata:    metadata,
	}
	return etagOf(value), nil
}

func (m *memBucket) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.objects[key]; !ok {
		return wasm.ErrNotFound
	}
	delete(m.objects, key)
	return nil
}

// List is the naive half of ListObjectsV2 — prefix, delimiter and a page — so
// the tests here can show the request and the listing crossing the ABI. What
// the SEMANTICS are is node/storage_test.go's, against the real index.
func (m *memBucket) List(_ context.Context, request wasm.ListRequest) (wasm.Listing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.asked = request
	if request.Token == "bad" {
		return wasm.Listing{}, fmt.Errorf("%w: not a token", wasm.ErrBadRequest)
	}

	var names []string
	for key := range m.objects {
		if strings.HasPrefix(key, request.Prefix) {
			names = append(names, key)
		}
	}
	sort.Strings(names)

	limit := int(request.MaxKeys)
	if limit <= 0 {
		limit = len(names)
	}

	var (
		listing wasm.Listing
		folded  = map[string]bool{}
	)
	for _, name := range names {
		if request.Token != "" && name <= request.Token {
			continue
		}
		if len(listing.Objects)+len(listing.CommonPrefixes) == limit {
			listing.Truncated = true
			listing.NextToken = "next"
			break
		}
		rest := strings.TrimPrefix(name, request.Prefix)
		if cut := strings.Index(rest, request.Delimiter); request.Delimiter != "" && cut >= 0 {
			prefix := request.Prefix + rest[:cut+len(request.Delimiter)]
			if !folded[prefix] {
				folded[prefix] = true
				listing.CommonPrefixes = append(listing.CommonPrefixes, prefix)
			}
			continue
		}
		listing.Objects = append(listing.Objects, m.info(name, m.objects[name]))
	}
	return listing, nil
}

// storing is a tier with one bucket and a workload granted every verb.
func storing(t *testing.T, ops ...string) (invoke.Instance, *buckets) {
	t.Helper()

	if len(ops) == 0 {
		ops = []string{"get", "head", "put", "delete", "list"}
	}
	store := newBuckets("h")
	instance := start(t, tier(t, cacher(t), wasm.WithStorage(store)), invoke.Spec{
		Name:    "fn",
		Reaches: []invoke.Grant{storageGrant("receipts", "h", ops...)},
	})
	return instance, store
}

// TestTHEBYTESAGuestWroteAreTheBytesItGets, which is the one claim no in-process
// fake can make on its own.
func TestAnObjectRoundTrips(t *testing.T) {
	instance, _ := storing(t)

	put := call(t, instance, "/obj-put/receipts/2026/01", []byte("a receipt"))
	require.Equal(t, 200, put.Status)

	// AND THE PUT ANSWERS THE ETAG, which is S3's contract and this cloud's
	// content id — so a caller records what it wrote without a second call.
	require.Equal(t, etagOf([]byte("a receipt")), string(put.Body))

	res := call(t, instance, "/obj-get/receipts/2026/01", nil)
	require.Equal(t, 200, res.Status)
	require.Equal(t, "a receipt", string(res.Body))
}

// TestTHEETAGIsTheContent, which is what an etag is for: it changes exactly
// when the bytes do, and two objects holding the same bytes share one.
func TestTwoObjectsWithOneContentShareAnEtag(t *testing.T) {
	instance, _ := storing(t)

	first := call(t, instance, "/obj-put/receipts/a", []byte("same"))
	second := call(t, instance, "/obj-put/receipts/b", []byte("same"))
	third := call(t, instance, "/obj-put/receipts/c", []byte("different"))

	require.Equal(t, string(first.Body), string(second.Body))
	require.NotEqual(t, string(first.Body), string(third.Body))
}

// TestTHECONTENTTYPEAndTheMetadataTravelWithTheBytes, which is the half of
// PutObject that makes an object servable over HTTP by anything else.
func TestAnObjectCarriesItsTypeAndMetadata(t *testing.T) {
	instance, _ := storing(t)

	res := callWith(t, instance, "/obj-put/receipts/k", []byte("{}"), map[string]string{
		"x-taubyte-content-type": "application/json",
		"x-meta-order":           "order-1",
		"x-meta-region":          "eu",
	})
	require.Equal(t, 200, res.Status, string(res.Body))

	// READ BACK THROUGH `get`, which is the record-inside-a-record: the info
	// travels beside the body rather than needing a second call.
	got := call(t, instance, "/obj-meta/receipts/k", nil)
	require.Equal(t, 200, got.Status, string(got.Body))
	require.Equal(t,
		"k:2:1700000000:"+etagOf([]byte("{}"))+":application/json:[order=order-1,region=eu]",
		string(got.Body))
}

// TestHEADIsTheWholeReasonThisIsNotKV: what is known about an object without
// pulling it through the ABI, as a record that has to survive lowering.
func TestHeadAnswersTheMetadataAlone(t *testing.T) {
	instance, _ := storing(t)

	require.Equal(t, 200, call(t, instance, "/obj-put/receipts/2026/01", []byte("0123456789")).Status)

	res := call(t, instance, "/obj-head/receipts/2026/01", nil)
	require.Equal(t, 200, res.Status)
	require.Equal(t,
		"2026/01:10:1700000000:"+etagOf([]byte("0123456789"))+"::[]",
		string(res.Body),
		"every field of the record the host lowered, read back inside the guest")
}

// TestALISTOfRecordsSurvivesTheABI, sorted, which is the second shape this
// interface carries that nothing before it did.
func TestEntriesComeBackAsRecords(t *testing.T) {
	instance, _ := storing(t)

	for name, body := range map[string]string{
		"2026/01": "jan", "2026/02": "feb", "2025/12": "dec",
	} {
		require.Equal(t, 200, call(t, instance, "/obj-put/receipts/"+name, []byte(body)).Status)
	}

	res := call(t, instance, "/obj-list/receipts/2026", nil)
	require.Equal(t, 200, res.Status)
	require.Equal(t, "2026/01:3\n2026/02:3", string(res.Body), "sorted, and 2025 not in it")
}

// TestADELIMITERCollapsesAPrefix, which is what makes a bucket browsable as
// folders — and is the reason S3's own listing takes one.
func TestAListingCollapsesOnTheDelimiter(t *testing.T) {
	instance, store := storing(t)

	for _, name := range []string{"2026/01", "2026/02", "2025/12", "top"} {
		require.Equal(t, 200, call(t, instance, "/obj-put/receipts/"+name, []byte("x")).Status)
	}

	res := callWith(t, instance, "/obj-list/receipts/", nil, map[string]string{
		"x-taubyte-delimiter": "/",
	})
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "top:1\n[2025/]\n[2026/]", string(res.Body),
		"the flat object, then the two prefixes everything else folded into")

	// AND THE WHOLE REQUEST RECORD CROSSED, not only the field the answer
	// happened to depend on.
	require.Equal(t, "/", store.byName["h"].asked.Delimiter)
}

// TestALISTINGIsPaged, which is the half of ListObjectsV2 a bucket with a
// million objects makes non-optional.
func TestAListingPagesAndSaysSo(t *testing.T) {
	instance, store := storing(t)

	for _, name := range []string{"a", "b", "c"} {
		require.Equal(t, 200, call(t, instance, "/obj-put/receipts/"+name, []byte("x")).Status)
	}

	res := callWith(t, instance, "/obj-list/receipts/", nil, map[string]string{
		"x-taubyte-max-keys": "2",
	})
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "a:1\nb:1\n...next", string(res.Body),
		"two objects, then the token that says there are more")

	require.Equal(t, uint32(2), store.byName["h"].asked.MaxKeys)

	// AND THE TOKEN GOES BACK, which is the loop every client's paging is.
	next := callWith(t, instance, "/obj-list/receipts/", nil, map[string]string{
		"x-taubyte-token": "b",
	})
	require.Equal(t, "c:1", string(next.Body))
	require.Equal(t, "b", store.byName["h"].asked.Token)
}

// TestATOKENThisBucketNeverIssuedIsItsOwnArm, so a caller FIXES it rather than
// retrying a listing that will refuse it forever.
func TestABadContinuationTokenIsNotAnOutage(t *testing.T) {
	instance, _ := storing(t)

	res := callWith(t, instance, "/obj-list/receipts/", nil, map[string]string{
		"x-taubyte-token": "bad",
	})
	require.Equal(t, 403, res.Status)
	require.True(t, strings.HasPrefix(string(res.Body), "bad-request:"), "got %q", res.Body)
}

// TestAMISSIsItsOwnArm, so a caller can tell "not there" from "the store is
// down" — which is the whole reason `not-found` is a case.
func TestAMissingObjectIsNotAnOutage(t *testing.T) {
	instance, _ := storing(t)

	res := call(t, instance, "/obj-get/receipts/nothing", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "not-found", string(res.Body))
}

// TestABUCKETAtItsSizeSaysSO, and says it in an arm of its own: a caller acts on
// full — write less, delete something — and an `unavailable` reads as an outage.
func TestAFullBucketIsItsOwnArm(t *testing.T) {
	instance, store := storing(t)
	store.byName["h"].full = 4

	res := call(t, instance, "/obj-put/receipts/big", []byte("more than four"))
	require.Equal(t, 403, res.Status)
	require.True(t, strings.HasPrefix(string(res.Body), "full:"), "got %q", res.Body)
}

// TestANOBJECTIsDeleted, and deleting one that is not there is a miss rather
// than a success.
func TestDeletingAnObject(t *testing.T) {
	instance, _ := storing(t)

	require.Equal(t, 200, call(t, instance, "/obj-put/receipts/k", []byte("x")).Status)
	require.Equal(t, 204, call(t, instance, "/obj-delete/receipts/k", nil).Status)
	require.Equal(t, "not-found", string(call(t, instance, "/obj-get/receipts/k", nil).Body))

	res := call(t, instance, "/obj-delete/receipts/k", nil)
	require.Equal(t, 403, res.Status)
	require.Equal(t, "not-found", string(res.Body))
}

// ── the grant ────────────────────────────────────────────────────────────────

// TestEVERYVerbIsCheckedSeparately. A read grant is not a write grant, and the
// check happens before the store is touched.
func TestAReadOnlyGrantCannotWrite(t *testing.T) {
	instance, store := storing(t, "get", "head", "list")

	res := call(t, instance, "/obj-put/receipts/k", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:receipts cannot put", string(res.Body))
	require.Empty(t, store.opened, "and nothing was opened to find that out")
}

// TestALABELTheConfigNeverGrantedIsRefusedByNAME, which reads as a typo rather
// than as an outage.
func TestAnUngrantedBucketIsRefusedByName(t *testing.T) {
	instance, _ := storing(t)

	res := call(t, instance, "/obj-get/somebody-elses/k", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "no-such-storage:somebody-elses", string(res.Body))
}

// TestAREFUSEDGrantCarriesTheReason, so an author is told which target and why
// rather than that a label does not exist.
func TestARefusedStorageGrantSaysWhy(t *testing.T) {
	store := newBuckets("h")
	instance := start(t, tier(t, cacher(t), wasm.WithStorage(store)), invoke.Spec{
		Name: "fn",
		Reaches: []invoke.Grant{{
			Kind: "storage", Label: "logs", Handle: "h", Ops: []string{"put"},
			Refused: "storage \"logs\" is streaming, and this cloud serves object storage",
		}},
	})

	res := call(t, instance, "/obj-put/logs/k", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "denied:")
	require.Contains(t, string(res.Body), "streaming")
	require.Empty(t, store.opened)
}

// TestAWORKLOADWhoseOnlyStorageIsREFUSEDStillStarts on a tier with no bucket
// backing — a per-target refusal must not become a per-workload one.
func TestARefusedStorageDoesNotNeedABacking(t *testing.T) {
	instance := start(t, tier(t, cacher(t)), invoke.Spec{
		Name: "fn",
		Reaches: []invoke.Grant{{
			Kind: "storage", Label: "logs", Ops: []string{"put"},
			Refused: "this cloud serves object storage and that one is streaming",
		}},
	})

	res := call(t, instance, "/obj-put/logs/k", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "streaming")
}

// TestANodeWithNothingToOpenABucketWithRefusesTheWorkload, which is the shape
// every other capability keeps: a servable grant with no backing is a
// composition fault rather than a call that fails later.
func TestAServableStorageGrantNeedsABacking(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy",
		Reaches: []invoke.Grant{storageGrant("receipts", "h", "put")},
	})
	require.ErrorIs(t, err, wasm.ErrNoStorage)
}

// TestTHEIMPORTIsRegisteredEvenWithNoGrant. An unprovided component import is a
// TRAP that takes the instance with it, so a guest built against the world runs
// whether or not it was granted anything.
func TestTheStorageImportExistsWithoutAGrant(t *testing.T) {
	instance := start(t, tier(t, cacher(t)), invoke.Spec{Name: "fn"})

	res := call(t, instance, "/obj-get/anything/k", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "no-such-storage:anything", string(res.Body))
}
