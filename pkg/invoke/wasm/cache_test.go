package wasm_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// EVERY TEST HERE DRIVES A REAL COMPONENT THROUGH THE CAPABILITY. The guest
// (fixtures/cacher) is built by cargo-component against the same wit the host
// implements, so what is proven is that the host's lowering and the generated
// bindings agree — including the arms of the error variant, which no fake could
// show.
//
// The guest reads "/<op>/<cache>/<key>" from the path and answers with the
// value, or with the NAME of the arm it got back. So an assertion on the body
// is an assertion about which refusal the host produced.

func cacher(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile("fixtures/cacher.wasm")
	require.NoError(t, err, "run `cargo component build --release` in fixtures/cacher")
	return code
}

// memCache is one cache. It is deliberately not pkg/cache: what this file
// proves is the ABI and the grant check, and a real store would put its own
// eviction between the guest and the assertion.
type memCache struct {
	mu     sync.Mutex
	values map[string][]byte
	err    error
}

func newMemCache() *memCache { return &memCache{values: map[string][]byte{}} }

func (m *memCache) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	v, ok := m.values[key]
	if !ok {
		return nil, wasm.ErrNotFound
	}
	return v, nil
}

func (m *memCache) Put(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.values[key] = value
	return nil
}

func (m *memCache) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	delete(m.values, key)
	return nil
}

func (m *memCache) List(_ context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}

	var out []string
	for k := range m.values {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, k)
		}
	}
	return out, nil
}

// caches records which handles were opened, so a test can assert that the
// LABEL the guest used resolved to the HANDLE the grant carried.
type caches struct {
	mu       sync.Mutex
	byName   map[string]*memCache
	opened   []string
	settings []map[string]string
	err      error
}

func newCaches(handles ...string) *caches {
	c := &caches{byName: map[string]*memCache{}}
	for _, h := range handles {
		c.byName[h] = newMemCache()
	}
	return c
}

func (c *caches) Open(_ context.Context, grant invoke.Grant) (wasm.Cache, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.opened = append(c.opened, grant.Handle)
	c.settings = append(c.settings, grant.Settings)
	if c.err != nil {
		return nil, c.err
	}
	cache, ok := c.byName[grant.Handle]
	if !ok {
		return nil, errors.New("no such handle: " + grant.Handle)
	}
	return cache, nil
}

func (c *caches) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.opened...)
}

// grants is the compiled access block a spec carries.
func grants(label, handle string, ops ...string) []invoke.Grant {
	return []invoke.Grant{{Kind: "cache", Label: label, Handle: handle, Ops: ops}}
}

func call(t *testing.T, instance invoke.Instance, path string, body []byte) *invoke.Response {
	t.Helper()
	res, err := instance.Serve(t.Context(), &invoke.Request{Method: "POST", Path: path, Body: body})
	require.NoError(t, err)
	return res
}

// ── the round trip ───────────────────────────────────────────────────────────

// TestAGuestWritesAndReadsItsCache — the whole point, through generated
// bindings, with the bytes crossing the ABI in both directions.
func TestAComponentReadsAndWritesACache(t *testing.T) {
	backing := newCaches("shop/prod/QmSessions")
	r := tier(t, cacher(t), wasm.WithCaches(backing))
	instance := start(t, r, invoke.Spec{
		Name:    "shop/prod/function/checkout",
		Reaches: grants("sessions", "shop/prod/QmSessions", "get", "put", "delete", "list"),
	})

	res := call(t, instance, "/roundtrip/sessions/user:1", []byte("alice"))
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "alice", string(res.Body), "the bytes the guest wrote are the bytes it got")

	// THE LABEL RESOLVED TO THE HANDLE, which is what makes one component
	// portable between environments.
	require.Equal(t, []string{"shop/prod/QmSessions", "shop/prod/QmSessions"}, backing.seen())
}

func TestEveryVerbCrossesTheABI(t *testing.T) {
	backing := newCaches("h")
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(backing)), invoke.Spec{
		Name:    "fn",
		Reaches: grants("sessions", "h", "get", "put", "delete", "list"),
	})

	require.Equal(t, 204, call(t, instance, "/put/sessions/a", []byte("1")).Status)
	require.Equal(t, 204, call(t, instance, "/put/sessions/b", []byte("2")).Status)

	res := call(t, instance, "/get/sessions/a", nil)
	require.Equal(t, 200, res.Status)
	require.Equal(t, "1", string(res.Body))

	res = call(t, instance, "/list/sessions/", nil)
	require.Equal(t, 200, res.Status)
	require.Contains(t, string(res.Body), "a")
	require.Contains(t, string(res.Body), "b")

	require.Equal(t, 204, call(t, instance, "/delete/sessions/a", nil).Status)

	res = call(t, instance, "/get/sessions/a", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "not-found", string(res.Body))
}

// TestAnEmptyValueSurvives, because a stored empty value is a legitimate
// answer and lowering it as nothing would trap.
func TestAnEmptyValueIsNotNothing(t *testing.T) {
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h"))), invoke.Spec{
		Name: "fn", Reaches: grants("sessions", "h", "get", "put"),
	})

	require.Equal(t, 204, call(t, instance, "/put/sessions/empty", nil).Status)

	res := call(t, instance, "/get/sessions/empty", nil)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Empty(t, res.Body)
}

// TestAValueLargerThanOnePageSurvivesBothDirections, so the list<u8> lowering
// is exercised past the size where a single-page copy would be enough — in and
// out, since a roundtrip lowers it into the guest, up to the host, and back.
//
// 24KiB AND NOT MORE, AND THE CEILING IS NOT THIS PACKAGE'S: see
// TestALargeValueHitsAPlatformCeilingThatIsNotOurs below.
func TestALargeValueSurvivesBothDirections(t *testing.T) {
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h"))), invoke.Spec{
		Name: "fn", Reaches: grants("sessions", "h", "get", "put"),
	})

	value := make([]byte, 24<<10)
	for i := range value {
		value[i] = byte(i % 251)
	}

	res := call(t, instance, "/roundtrip/sessions/big", value)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, value, res.Body)
}

// TestALargeValueHitsAPlatformCeilingThatIsNotOurs, AND IT IS A DEFECT RATHER
// THAN A DESIGN — measured here so it is known rather than discovered.
//
// wazy's canonical-ABI lowering bounds-checks an allocation against a Go slice
// of the guest's memory CAPTURED BEFORE cabi_realloc runs: allocStoreString,
// allocStoreBytes and allocStoreList in internal/component/abi/memory.go all
// compare `len(mem)` to a pointer the realloc returned. A realloc that GROWS
// the memory invalidates that slice, so any value whose allocation crosses the
// guest's current memory end is rejected as "allocated memory out of bounds"
// even though the growth succeeded.
//
// So the limit is not a size, it is "whatever is left below the end", and it
// moves with what the instance has already allocated — which is why this test
// searches rather than asserting a number. `execution/memory` does not raise
// it, because the memory limit is not what is being hit.
//
// WHAT IT COSTS THIS PLATFORM: no wasm workload can be handed a request body,
// or hand back a cache value, past a few tens of kilobytes. Every serverless
// platform this competes with accepts megabytes.
//
// The fix is one line at each of the three sites — re-read the memory after
// Grow — and it belongs in wazy, which this repository does not write to.
func TestALargeValueHitsAPlatformCeilingThatIsNotOurs(t *testing.T) {
	var largestThatWorked int

	for size := 4 << 10; size <= 8<<20; size *= 2 {
		// A FRESH INSTANCE EACH TIME, because the ceiling is what is left
		// below the end and a previous call moved it.
		instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h"))), invoke.Spec{
			Name: "fn", Reaches: grants("sessions", "h", "get", "put"),
		})

		_, err := instance.Serve(t.Context(), &invoke.Request{
			Method: "POST", Path: "/roundtrip/sessions/big", Body: make([]byte, size),
		})
		if err != nil {
			require.ErrorContains(t, err, "allocated memory out of bounds",
				"a large value failed for some OTHER reason, which is worth reading")
			t.Logf("the ceiling is between %d and %d bytes", largestThatWorked, size)
			return
		}
		largestThatWorked = size
	}

	t.Fatalf("8MiB crossed intact: wazy grew the memory correctly and the ceiling is gone. "+
		"Delete this test and raise the sizes in the ones around it. (largest tried: %d)", largestThatWorked)
}

// ── the grant is the authorization ───────────────────────────────────────────

// TestACacheNobodyDeclaredIsRefusedBeforeTheStoreIsTouched. The configuration
// is the grant, and there is no second place to widen it.
func TestAnUndeclaredCacheIsRefused(t *testing.T) {
	backing := newCaches("h")
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(backing)), invoke.Spec{
		Name: "fn", Reaches: grants("sessions", "h", "get", "put"),
	})

	res := call(t, instance, "/get/somebody-elses/k", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "no-such-cache:somebody-elses", string(res.Body))
	require.Empty(t, backing.seen(), "nothing was opened")
}

// TestAVerbNobodyAskedForIsRefused. A resource that declared `ops: [get]` reads
// and does not write, and the refusal names which.
func TestAnUngrantedVerbIsDenied(t *testing.T) {
	backing := newCaches("h")
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(backing)), invoke.Spec{
		Name: "fn", Reaches: grants("sessions", "h", "get"),
	})

	res := call(t, instance, "/put/sessions/k", []byte("v"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:sessions cannot put", string(res.Body))

	res = call(t, instance, "/delete/sessions/k", nil)
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:sessions cannot delete", string(res.Body))

	res = call(t, instance, "/list/sessions/", nil)
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:sessions cannot list", string(res.Body))

	require.Empty(t, backing.seen(), "a denied op opened nothing")

	// And the one it did ask for still works.
	require.Equal(t, 404, call(t, instance, "/get/sessions/k", nil).Status)
	require.Equal(t, []string{"h"}, backing.seen())
}

// TestWatchIsNotImplementedAndSaysSo. The verb exists in the vocabulary, the
// host ABI never had an export for it, and a resource asking for it gets the
// op check's answer rather than silence.
func TestAGrantForWatchBuysNothing(t *testing.T) {
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h"))), invoke.Spec{
		Name: "fn", Reaches: grants("sessions", "h", "watch"),
	})

	res := call(t, instance, "/get/sessions/k", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "denied:sessions cannot get", string(res.Body))
}

// TestAGrantForAnotherKindIsNotACacheGrant, so a `storage:` entry cannot be
// spent on a cache.
func TestAGrantOfAnotherKindDoesNotOpenACache(t *testing.T) {
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h"))), invoke.Spec{
		Name: "fn",
		Reaches: []invoke.Grant{
			{Kind: "storage", Label: "sessions", Handle: "h", Ops: []string{"get", "put"}},
			{Kind: "cache", Label: "other", Handle: "h", Ops: []string{"get"}},
		},
	})

	res := call(t, instance, "/get/sessions/k", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "no-such-cache:sessions", string(res.Body))
}

// ── when there is nothing behind the grant ───────────────────────────────────

// TestAGrantWithNoBackingIsRefusedAtStart, not at the first call: a workload
// that would fail every request touching its cache should not start and then
// look healthy until somebody exercises that path.
func TestATierWithNoCachesRefusesAWorkloadThatNeedsOne(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy", Reaches: grants("sessions", "h", "get"),
	})
	require.ErrorIs(t, err, wasm.ErrNoCaches)
}

// TestATierWithNoCachesStillRunsEverythingElse, because most workloads reach
// no cache and a tier that refused them all would be useless.
//
// AND A GUEST THAT CALLS ONE ANYWAY IS REFUSED RATHER THAN KILLED. An import a
// component declares and the host does not provide is a trap that takes the
// instance with it, so the four functions are registered even when nothing is
// granted — a guest bug is not a reason to lose warm state.
func TestATierWithNoCachesRunsAWorkloadThatNeedsNone(t *testing.T) {
	instance := start(t, tier(t, cacher(t)), invoke.Spec{Name: "fn"})

	res := call(t, instance, "/get/sessions/k", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "no-such-cache:sessions", string(res.Body))

	// And the instance is still alive, which is the half a trap would have
	// taken.
	res = call(t, instance, "/get/sessions/other", nil)
	require.Equal(t, 404, res.Status)
}

// ── when the store fails ─────────────────────────────────────────────────────

// TestAStoreFailureIsNotAMiss. A caller retries one and recomputes for the
// other, so folding an outage into `not-found` would make every miss look like
// a success and every outage look like a miss.
func TestAnUnreachableStoreIsItsOwnArm(t *testing.T) {
	backing := newCaches("h")
	backing.byName["h"].err = errors.New("the swarm is unreachable")

	instance := start(t, tier(t, cacher(t), wasm.WithCaches(backing)), invoke.Spec{
		Name: "fn", Reaches: grants("sessions", "h", "get", "put"),
	})

	res := call(t, instance, "/get/sessions/k", nil)
	require.Equal(t, 404, res.Status)
	require.Contains(t, string(res.Body), "unavailable:")
	require.Contains(t, string(res.Body), "the swarm is unreachable",
		"the store's own words reach the author, who owns both sides")
}

// TestACacheThatCannotBeOpenedIsUnavailable, which is different from one that
// was never granted.
func TestAnOpenFailureIsUnavailable(t *testing.T) {
	backing := newCaches()
	backing.err = errors.New("no store on this node")

	instance := start(t, tier(t, cacher(t), wasm.WithCaches(backing)), invoke.Spec{
		Name: "fn", Reaches: grants("sessions", "h", "get"),
	})

	res := call(t, instance, "/get/sessions/k", nil)
	require.Equal(t, 404, res.Status)
	require.Contains(t, string(res.Body), "unavailable:no store on this node")
}

// TestAValueTooLargeIsItsOwnArm, so an author can tell "it will never fit"
// from "it is not there".
func TestTooLargeIsItsOwnArm(t *testing.T) {
	backing := newCaches("h")
	backing.byName["h"].err = wasm.ErrTooLarge

	instance := start(t, tier(t, cacher(t), wasm.WithCaches(backing)), invoke.Spec{
		Name: "fn", Reaches: grants("sessions", "h", "put"),
	})

	res := call(t, instance, "/put/sessions/k", []byte("v"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "too-large", string(res.Body))
}

// ── two caches, two grants ───────────────────────────────────────────────────

// TestOneWorkloadReachesTwoCaches, each by its own label, each resolving to its
// own handle.
func TestTwoLabelsResolveToTwoCaches(t *testing.T) {
	backing := newCaches("QmSessions", "QmOrders")
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(backing)), invoke.Spec{
		Name: "fn",
		Reaches: []invoke.Grant{
			{Kind: "cache", Label: "sessions", Handle: "QmSessions", Ops: []string{"get", "put"}},
			{Kind: "cache", Label: "orders", Handle: "QmOrders", Ops: []string{"get", "put"}},
		},
	})

	require.Equal(t, 204, call(t, instance, "/put/sessions/k", []byte("mine")).Status)
	require.Equal(t, 204, call(t, instance, "/put/orders/k", []byte("theirs")).Status)

	res := call(t, instance, "/get/sessions/k", nil)
	require.Equal(t, "mine", string(res.Body))
	res = call(t, instance, "/get/orders/k", nil)
	require.Equal(t, "theirs", string(res.Body))
}

// ── a filesystem a component never gets ──────────────────────────────────────

// TestAComponentThatDeclaresAMountIsRefused. The sandbox preopens no directory
// on purpose, so a guest started with a declared mount would write into a WASI
// shim's memory and lose it at the end of the INSTANCE — sooner even than the
// container tier's, which loses it at the end of the container.
func TestAComponentDeclaringAMountIsRefused(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy",
		Reaches: []invoke.Grant{{
			Kind: invoke.MountKind, Label: "pgdata", Ops: []string{"mount-rw"},
			Settings: map[string]string{"path": "/data"},
		}},
	})

	require.ErrorIs(t, err, wasm.ErrNoMounts)
	require.ErrorContains(t, err, "pgdata")
	require.ErrorContains(t, err, "no filesystem")
}

// TestAGrantOfAnotherKindStillStarts, so the refusal is about mounts and not
// about access blocks.
//
// `storage` and not `database`: a kind this tier serves refuses at Start when
// nothing backs it, which is a different rule and has its own test.
func TestAComponentWithOtherGrantsStarts(t *testing.T) {
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h"))), invoke.Spec{
		Name: "fn",
		Reaches: []invoke.Grant{
			{Kind: "cache", Label: "sessions", Handle: "h", Ops: []string{"get"}},
			{Kind: "storage", Label: "receipts", Ops: []string{"put"}},
		},
	})
	require.Equal(t, 404, call(t, instance, "/get/sessions/k", nil).Status)
}
