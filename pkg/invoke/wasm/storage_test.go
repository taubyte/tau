package wasm_test

import (
	"context"
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
// only thing that shows the record this interface returns survives both
// directions of the ABI: `head` and `entries` are the first functions here to
// lower a record, and a shape that lifts wrong is a field read off the wrong
// offset rather than an error.

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
	body     []byte
	modified int64
}

type memBucket struct {
	mu      sync.Mutex
	objects map[string]storedObject

	// full, when set, is the size this bucket refuses to grow past.
	full int64
}

func (m *memBucket) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	object, ok := m.objects[key]
	if !ok {
		return nil, wasm.ErrNotFound
	}
	return object.body, nil
}

func (m *memBucket) Head(_ context.Context, key string) (wasm.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	object, ok := m.objects[key]
	if !ok {
		return wasm.Object{}, wasm.ErrNotFound
	}
	return wasm.Object{Name: key, Size: int64(len(object.body)), Modified: object.modified}, nil
}

func (m *memBucket) Put(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.full > 0 && int64(len(value)) > m.full {
		return fmt.Errorf("%w: %d bytes over", wasm.ErrFull, int64(len(value))-m.full)
	}
	m.objects[key] = storedObject{body: value, modified: 1_700_000_000}
	return nil
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

func (m *memBucket) Entries(_ context.Context, prefix string) ([]wasm.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []wasm.Object
	for key, object := range m.objects {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		out = append(out, wasm.Object{Name: key, Size: int64(len(object.body)), Modified: object.modified})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
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

	require.Equal(t, 204, call(t, instance, "/obj-put/receipts/2026/01", []byte("a receipt")).Status)

	res := call(t, instance, "/obj-get/receipts/2026/01", nil)
	require.Equal(t, 200, res.Status)
	require.Equal(t, "a receipt", string(res.Body))
}

// TestHEADIsTheWholeReasonThisIsNotKV: what is known about an object without
// pulling it through the ABI, as a record that has to survive lowering.
func TestHeadAnswersTheMetadataAlone(t *testing.T) {
	instance, _ := storing(t)

	require.Equal(t, 204, call(t, instance, "/obj-put/receipts/2026/01", []byte("0123456789")).Status)

	res := call(t, instance, "/obj-head/receipts/2026/01", nil)
	require.Equal(t, 200, res.Status)
	require.Equal(t, "2026/01:10:1700000000", string(res.Body),
		"name, size and modified, read back off the record the host lowered")
}

// TestALISTOfRecordsSurvivesTheABI, sorted, which is the second shape this
// interface carries that nothing before it did.
func TestEntriesComeBackAsRecords(t *testing.T) {
	instance, _ := storing(t)

	for name, body := range map[string]string{
		"2026/01": "jan", "2026/02": "feb", "2025/12": "dec",
	} {
		require.Equal(t, 204, call(t, instance, "/obj-put/receipts/"+name, []byte(body)).Status)
	}

	res := call(t, instance, "/obj-list/receipts/2026", nil)
	require.Equal(t, 200, res.Status)
	require.Equal(t, "2026/01:3\n2026/02:3", string(res.Body), "sorted, and 2025 not in it")
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

	require.Equal(t, 204, call(t, instance, "/obj-put/receipts/k", []byte("x")).Status)
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
