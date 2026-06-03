//go:build wasmtime_component

package wasmtimehttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/taubyte/tau/services/substrate/components/http/website"
)

// TestServeHTTPProxiesAndCaches drives the backend with a stub "component
// server" (a Go httptest server) in place of `wasmtime serve`, so the proxy and
// per-key caching are tested without the wasmtime binary.
func TestServeHTTPProxiesAndCaches(t *testing.T) {
	var launches int32
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("content-type", "text/plain")
		w.WriteHeader(201)
		io.WriteString(w, r.Method+" "+r.URL.Path+" body="+string(body))
	}))
	defer stub.Close()
	stubAddr := strings.TrimPrefix(stub.URL, "http://")

	rt := New()
	defer rt.Close()
	rt.launch = func(ctx context.Context, wasmPath string, limits website.ComponentLimits) (string, func(), error) {
		atomic.AddInt32(&launches, 1)
		return stubAddr, func() {}, nil
	}

	do := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://site.example"+path, strings.NewReader(body))
		w := httptest.NewRecorder()
		if err := rt.ServeHTTP(context.Background(), "cidABC", []byte("\x00asm-component"), w, r, website.ComponentLimits{}); err != nil {
			t.Fatalf("ServeHTTP: %v", err)
		}
		return w
	}

	// First request: proxied to the stub, response passed back through.
	w := do("POST", "/api/x", "hello")
	if w.Code != 201 {
		t.Errorf("status = %d, want 201", w.Code)
	}
	if got := w.Body.String(); got != "POST /api/x body=hello" {
		t.Errorf("proxied body = %q", got)
	}
	if ct := w.Header().Get("content-type"); ct != "text/plain" {
		t.Errorf("content-type not proxied: %q", ct)
	}

	// Second request, same component: must reuse the server (launch once).
	_ = do("GET", "/other", "")
	if n := atomic.LoadInt32(&launches); n != 1 {
		t.Errorf("launched %d times, want 1 (cached per component)", n)
	}

	// A different component triggers another launch.
	r := httptest.NewRequest("GET", "http://site.example/", nil)
	if err := rt.ServeHTTP(context.Background(), "cidDEF", []byte("\x00asm2"), httptest.NewRecorder(), r, website.ComponentLimits{}); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&launches); n != 2 {
		t.Errorf("launched %d times for 2 components, want 2", n)
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
