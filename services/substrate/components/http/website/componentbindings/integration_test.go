//go:build wasmtime_component

package componentbindings

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	dbIface "github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/services/substrate/components/http/website"
	"github.com/taubyte/tau/services/substrate/components/http/website/bindings"
	"github.com/taubyte/tau/services/substrate/components/http/website/wasmtimehttp"
)

// TestKVBindingThroughComponent drives the full real chain: a StarlingMonkey
// component's env.KV.put/get -> the loopback binding server -> the real
// componentbindings.NewKV adapter -> a database service (a faithful in-memory
// fake). It proves a value written by the component persists and reads back,
// exercising the adapter exactly as the substrate wires it.
//
// Gated on TAUBYTE_TEST_KV_COMPONENT (the counter example built with
// `--engine starlingmonkey`) + wasmtime on PATH.
func TestKVBindingThroughComponent(t *testing.T) {
	comp := os.Getenv("TAUBYTE_TEST_KV_COMPONENT")
	if comp == "" {
		t.Skip("set TAUBYTE_TEST_KV_COMPONENT to the env.KV counter component .wasm")
	}
	data, err := os.ReadFile(comp)
	if err != nil {
		t.Fatal(err)
	}

	// Real binding server, backed by the real adapter over a faithful DB fake.
	server, err := bindings.NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	kv := &fakeKV{m: map[string][]byte{}}
	svc := fakeDBService{db: &fakeDB{kv: kv}}
	token, err := server.Registry().Add(func() *bindings.Scope {
		return &bindings.Scope{
			KV: map[string]bindings.KV{
				"KV": NewKV(svc, context.Background(), dbIface.Context{ProjectId: "p", Matcher: "m"}),
			},
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	// x-taubyte-bindings is the JSON the shim parses: a base URL + the kv binding
	// names. env.KV -> <base>/kv/KV/<key>.
	bindingsHeader := `{"base":"` + server.URLFor(token) + `","kv":["KV"]}`

	rt := wasmtimehttp.New()
	defer rt.Close()

	count := func() int {
		r := httptest.NewRequest("GET", "http://site.example/", nil)
		r.Header.Set("x-taubyte-bindings", bindingsHeader)
		w := httptest.NewRecorder()
		if err := rt.ServeHTTP(context.Background(), "kvcounter", data, w, r, website.ComponentLimits{}); err != nil {
			t.Fatalf("ServeHTTP: %v", err)
		}
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var out struct {
			Count int `json:"count"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("bad json %q: %v", w.Body.String(), err)
		}
		return out.Count
	}

	// Each request increments the KV-backed counter: 1, then 2, then 3 — proving
	// put+get persist through the adapter into the (fake) database.
	for want := 1; want <= 3; want++ {
		if got := count(); got != want {
			t.Fatalf("count = %d, want %d", got, want)
		}
	}
	// And the value really lives in the backing store.
	if v, _ := kv.m["count"]; string(v) != "3" {
		t.Errorf("backing KV count = %q, want 3", v)
	}
	t.Log("env.KV.put/get round-trips through the real adapter into the database")
}
