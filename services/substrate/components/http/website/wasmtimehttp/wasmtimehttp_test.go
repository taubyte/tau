//go:build wasmtime_component

package wasmtimehttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taubyte/tau/services/substrate/components/http/website"
)

// stubBackends installs a launch func that, per call, starts a small httptest
// server tagged with a unique instance id (so round-robin is observable) and
// records launches. Returns the runtime and a launch counter.
func stubBackends(t *testing.T, rt *Runtime) *int32 {
	t.Helper()
	var launches int32
	var mu sync.Mutex
	var servers []*httptest.Server
	t.Cleanup(func() {
		for _, s := range servers {
			s.Close()
		}
	})
	rt.launch = func(wasmPath string, limits website.ComponentLimits) (string, func(), *atomic.Bool, error) {
		id := atomic.AddInt32(&launches, 1)
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("content-type", "text/plain")
			w.WriteHeader(201)
			fmt.Fprintf(w, "inst%d %s %s body=%s", id, r.Method, r.URL.Path, string(body))
		}))
		mu.Lock()
		servers = append(servers, s)
		mu.Unlock()
		alive := &atomic.Bool{}
		alive.Store(true)
		return strings.TrimPrefix(s.URL, "http://"), s.Close, alive, nil
	}
	return &launches
}

func do(t *testing.T, rt *Runtime, key, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://site.example"+path, strings.NewReader(body))
	w := httptest.NewRecorder()
	if err := rt.ServeHTTP(context.Background(), key, []byte("\x00asm"+key), w, r, website.ComponentLimits{}); err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
	return w
}

func TestServeHTTPProxiesAndCaches(t *testing.T) {
	rt := New()
	defer rt.Close()
	launches := stubBackends(t, rt)

	w := do(t, rt, "cidABC", "POST", "/api/x", "hello")
	if w.Code != 201 {
		t.Errorf("status = %d, want 201", w.Code)
	}
	if got := w.Body.String(); !strings.Contains(got, "POST /api/x body=hello") {
		t.Errorf("proxied body = %q", got)
	}
	if ct := w.Header().Get("content-type"); ct != "text/plain" {
		t.Errorf("content-type not proxied: %q", ct)
	}

	// Same component reuses its pool (poolSize 1 -> one launch).
	_ = do(t, rt, "cidABC", "GET", "/other", "")
	if n := atomic.LoadInt32(launches); n != 1 {
		t.Errorf("launched %d times, want 1 (cached per component)", n)
	}
	// A different component launches its own.
	_ = do(t, rt, "cidDEF", "GET", "/", "")
	if n := atomic.LoadInt32(launches); n != 2 {
		t.Errorf("launched %d for 2 components, want 2", n)
	}
}

func TestPoolFillsAndRoundRobins(t *testing.T) {
	rt := New()
	defer rt.Close()
	rt.poolSize = 3
	launches := stubBackends(t, rt)

	// First request fills the pool to poolSize.
	_ = do(t, rt, "cid", "GET", "/", "")
	if n := atomic.LoadInt32(launches); n != 3 {
		t.Fatalf("pool launched %d instances, want 3", n)
	}

	// Subsequent requests round-robin: across several calls we should see more
	// than one distinct backend id.
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		body := do(t, rt, "cid", "GET", "/", "").Body.String()
		seen[strings.Fields(body)[0]] = true // "instN"
	}
	if len(seen) < 2 {
		t.Errorf("round-robin hit only %v, want >=2 distinct instances", seen)
	}
	if n := atomic.LoadInt32(launches); n != 3 {
		t.Errorf("launched %d after warm pool, want 3 (no re-spawn)", n)
	}
}

func TestRespawnsDeadInstance(t *testing.T) {
	rt := New()
	defer rt.Close()
	launches := stubBackends(t, rt)

	_ = do(t, rt, "cid", "GET", "/", "")
	if n := atomic.LoadInt32(launches); n != 1 {
		t.Fatalf("want 1 launch, got %d", n)
	}

	// Mark the live instance dead; the next request must prune + respawn it.
	rt.pools["cid"].insts[0].alive.Store(false)
	_ = do(t, rt, "cid", "GET", "/", "")
	if n := atomic.LoadInt32(launches); n != 2 {
		t.Errorf("dead instance not respawned: launches = %d, want 2", n)
	}
}

func TestEvictsIdle(t *testing.T) {
	rt := New()
	defer rt.Close()
	rt.idleTTL = 10 * time.Millisecond
	stubBackends(t, rt)

	_ = do(t, rt, "cid", "GET", "/", "")
	rt.mu.Lock()
	have := len(rt.pools)
	rt.mu.Unlock()
	if have != 1 {
		t.Fatalf("pools = %d, want 1", have)
	}

	time.Sleep(25 * time.Millisecond)
	rt.evictIdle()

	rt.mu.Lock()
	have = len(rt.pools)
	rt.mu.Unlock()
	if have != 0 {
		t.Errorf("idle component not evicted: pools = %d, want 0", have)
	}
}

func TestEvictsLRUAtCap(t *testing.T) {
	rt := New()
	defer rt.Close()
	rt.maxComps = 2
	stubBackends(t, rt)

	_ = do(t, rt, "a", "GET", "/", "")
	_ = do(t, rt, "b", "GET", "/", "")
	_ = do(t, rt, "c", "GET", "/", "") // evicts "a" (LRU)

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.pools) != 2 {
		t.Errorf("pools = %d, want 2 (capped)", len(rt.pools))
	}
	if _, ok := rt.pools["a"]; ok {
		t.Error("LRU component `a` should have been evicted")
	}
	if _, ok := rt.pools["c"]; !ok {
		t.Error("newest component `c` should be present")
	}
}

func TestFreePort(t *testing.T) {
	p, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	if p <= 0 || p > 65535 {
		t.Errorf("freePort returned %d", p)
	}
}

func TestSanitize(t *testing.T) {
	if got := sanitize("Qm/abc:def"); strings.ContainsAny(got, "/:") {
		t.Errorf("sanitize left unsafe chars: %q", got)
	}
}

// TestRealWasmtimeServe exercises the real spawnWasmtime path against an actual
// wasi:http component. Gated on TAUBYTE_TEST_COMPONENT (a .wasm) + wasmtime on
// PATH; skips otherwise so it stays out of normal CI.
func TestRealWasmtimeServe(t *testing.T) {
	comp := os.Getenv("TAUBYTE_TEST_COMPONENT")
	if comp == "" {
		t.Skip("set TAUBYTE_TEST_COMPONENT to a wasi:http component .wasm")
	}
	data, err := os.ReadFile(comp)
	if err != nil {
		t.Fatal(err)
	}
	rt := New()
	defer rt.Close()
	r := httptest.NewRequest("GET", "http://site.example/products/42", nil)
	w := httptest.NewRecorder()
	if err := rt.ServeHTTP(context.Background(), "real", data, w, r, website.ComponentLimits{}); err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	t.Logf("component responded %d: %.120s", w.Code, w.Body.String())
}

// TestComponentBindings drives a component built from the bindings example
// (returns env.MY_SECRET and env.KV.get("greeting")) through the backend with
// the substrate's binding headers injected: x-taubyte-env for secrets and
// x-taubyte-bindings pointing at an in-process KV endpoint the component fetches
// (outbound). Gated on TAUBYTE_TEST_BINDINGS_COMPONENT + wasmtime on PATH.
func TestComponentBindings(t *testing.T) {
	comp := os.Getenv("TAUBYTE_TEST_BINDINGS_COMPONENT")
	if comp == "" {
		t.Skip("set TAUBYTE_TEST_BINDINGS_COMPONENT to the bindings example component .wasm")
	}
	data, err := os.ReadFile(comp)
	if err != nil {
		t.Fatal(err)
	}

	// Stand-in for the substrate KV binding endpoint (named binding "KV").
	bindings := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/kv/KV/greeting" {
			io.WriteString(w, "hello-from-kv")
			return
		}
		w.WriteHeader(404)
	}))
	defer bindings.Close()

	rt := New()
	defer rt.Close()

	bindingsHeader := `{"base":"` + bindings.URL + `","kv":["KV"]}`
	serve := func(path string) map[string]any {
		r := httptest.NewRequest("GET", "http://site.example"+path, nil)
		r.Header.Set("x-taubyte-env", `{"MY_SECRET":"s3cr3t"}`)
		r.Header.Set("x-taubyte-bindings", bindingsHeader)
		w := httptest.NewRecorder()
		if err := rt.ServeHTTP(context.Background(), "bind", data, w, r, website.ComponentLimits{}); err != nil {
			t.Fatalf("ServeHTTP %s: %v", path, err)
		}
		if w.Code != 200 {
			t.Fatalf("%s: status %d, body %s", path, w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: bad json %q: %v", path, w.Body.String(), err)
		}
		return out
	}

	// Secret arrives via x-taubyte-env; env.KV is present.
	root := serve("/")
	if root["secret"] != "s3cr3t" {
		t.Errorf("env.MY_SECRET = %v, want s3cr3t", root["secret"])
	}
	if root["hasKV"] != true {
		t.Errorf("env.KV missing: %v", root["hasKV"])
	}
	// env.KV.get fetches the binding endpoint.
	kv := serve("/kv")
	if kv["kv"] != "hello-from-kv" {
		t.Errorf("env.KV.get(greeting) = %v, want hello-from-kv", kv["kv"])
	}
	t.Logf("bindings ok: secret=%v kv=%v", root["secret"], kv["kv"])
}
