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

// Every test drives a real component (fixtures/cacher, built against the same
// wit), so lowering and the error arms are shown to agree with generated bindings.
// The guest reads "/<op>/<cache>/<key>" and answers with the value or the arm's
// name.

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
	return callWith(t, instance, path, body, nil)
}

// callWith is the same, with headers — which is how the fixture is told the
// knobs a call has more than two of: an object's content type, a listing's
// delimiter and page.
func callWith(t *testing.T, instance invoke.Instance, path string, body []byte, headers map[string]string) *invoke.Response {
	t.Helper()

	req := &invoke.Request{Method: "POST", Path: path, Body: body}
	if len(headers) > 0 {
		req.Headers = map[string][]string{}
		for name, value := range headers {
			req.Headers[name] = []string{value}
		}
	}

	res, err := instance.Serve(t.Context(), req)
	require.NoError(t, err)
	return res
}

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

func TestAnEmptyValueIsNotNothing(t *testing.T) {
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h"))), invoke.Spec{
		Name: "fn", Reaches: grants("sessions", "h", "get", "put"),
	})

	require.Equal(t, 204, call(t, instance, "/put/sessions/empty", nil).Status)

	res := call(t, instance, "/get/sessions/empty", nil)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Empty(t, res.Body)
}

func TestALargeValueSurvivesBothDirections(t *testing.T) {
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h"))), invoke.Spec{
		Name: "fn", Memory: 32 << 20, Reaches: grants("sessions", "h", "get", "put"),
	})

	value := make([]byte, 1<<20)
	for i := range value {
		value[i] = byte(i % 251)
	}

	res := call(t, instance, "/roundtrip/sessions/big", value)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, value, res.Body)
}

func TestAMultiMegabyteBodyCrosses(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{Name: "fn", Memory: 64 << 20})

	body := make([]byte, 6<<20) // AWS Lambda's own synchronous ceiling.
	for i := range body {
		body[i] = byte(i % 251)
	}

	res, err := instance.Serve(t.Context(), &invoke.Request{Method: "GET", Path: "/", Body: body})
	require.NoError(t, err)
	require.Equal(t, append([]byte("GET "), body...), res.Body)
}

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

func TestAGrantForWatchBuysNothing(t *testing.T) {
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h"))), invoke.Spec{
		Name: "fn", Reaches: grants("sessions", "h", "watch"),
	})

	res := call(t, instance, "/get/sessions/k", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "denied:sessions cannot get", string(res.Body))
}

func TestAGrantOfAnotherKindDoesNotOpenACache(t *testing.T) {
	instance := start(t, tier(t,
		cacher(t),
		wasm.WithCaches(newCaches("h")),
		// The storage grant is servable, so it needs a backing; it is real and still not a
		// cache.
		wasm.WithStorage(newBuckets()),
	), invoke.Spec{
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

func TestATierWithNoCachesRefusesAWorkloadThatNeedsOne(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy", Reaches: grants("sessions", "h", "get"),
	})
	require.ErrorIs(t, err, wasm.ErrNoCaches)
}

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

func TestAComponentWithOtherGrantsStarts(t *testing.T) {
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h"))), invoke.Spec{
		Name: "fn",
		Reaches: []invoke.Grant{
			{Kind: "cache", Label: "sessions", Handle: "h", Ops: []string{"get"}},
			// A kind this tier has no interface for.
			{Kind: "mcp", Label: "tools", Ops: []string{"list"}},
		},
	})
	require.Equal(t, 404, call(t, instance, "/get/sessions/k", nil).Status)
}
