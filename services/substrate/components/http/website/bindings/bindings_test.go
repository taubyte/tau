package bindings

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
)

// memKV / memStorage are in-memory fakes standing in for the Taubyte-backed
// implementations.
type memKV struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMemKV() *memKV { return &memKV{m: map[string][]byte{}} }

func (k *memKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[key]
	return v, ok, nil
}
func (k *memKV) Put(_ context.Context, key string, value []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = value
	return nil
}
func (k *memKV) Delete(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, key)
	return nil
}
func (k *memKV) List(_ context.Context, prefix string) ([]string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	var keys []string
	for key := range k.m {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

type memStorage struct{ m map[string][]byte }

func (s *memStorage) Get(_ context.Context, path string) ([]byte, string, bool, error) {
	v, ok := s.m[path]
	return v, "application/octet-stream", ok, nil
}
func (s *memStorage) Put(_ context.Context, path string, data []byte) error {
	s.m[path] = data
	return nil
}

func TestBindingServerKVRoundTrip(t *testing.T) {
	srv, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	kv := newMemKV()
	token, err := srv.Registry().Add(func() *Scope { return &Scope{KV: map[string]KV{"MY_KV": kv}} })
	if err != nil {
		t.Fatal(err)
	}
	base := srv.URLFor(token) + "/kv/MY_KV"

	// miss
	if code, _ := req(t, "GET", base+"/missing", ""); code != 404 {
		t.Errorf("GET missing = %d, want 404", code)
	}
	// put
	if code, _ := req(t, "PUT", base+"/greeting", "hello"); code != 204 {
		t.Errorf("PUT = %d, want 204", code)
	}
	// get
	if code, body := req(t, "GET", base+"/greeting", ""); code != 200 || body != "hello" {
		t.Errorf("GET = %d %q, want 200 hello", code, body)
	}
	// list
	req(t, "PUT", base+"/greeting2", "hi")
	if code, body := req(t, "GET", base+"?prefix=greet", ""); code != 200 {
		t.Errorf("LIST = %d", code)
	} else {
		var keys []string
		json.Unmarshal([]byte(body), &keys)
		if len(keys) != 2 {
			t.Errorf("list = %v, want 2 keys", keys)
		}
	}
	// delete
	if code, _ := req(t, "DELETE", base+"/greeting", ""); code != 204 {
		t.Errorf("DELETE = %d, want 204", code)
	}
	if code, _ := req(t, "GET", base+"/greeting", ""); code != 404 {
		t.Errorf("GET after delete = %d, want 404", code)
	}
}

func TestBindingServerStorage(t *testing.T) {
	srv, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	st := &memStorage{m: map[string][]byte{}}
	token, _ := srv.Registry().Add(func() *Scope { return &Scope{Storage: map[string]Storage{"FILES": st}} })
	base := srv.URLFor(token) + "/storage/FILES"

	if code, _ := req(t, "PUT", base+"/a/b.txt", "filedata"); code != 204 {
		t.Errorf("PUT storage = %d, want 204", code)
	}
	if code, body := req(t, "GET", base+"/a/b.txt", ""); code != 200 || body != "filedata" {
		t.Errorf("GET storage = %d %q, want 200 filedata", code, body)
	}
}

func TestBindingServerTokenScoping(t *testing.T) {
	srv, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	a := newMemKV()
	a.Put(context.Background(), "secret", []byte("A-data"))
	tokenA, _ := srv.Registry().Add(func() *Scope { return &Scope{KV: map[string]KV{"KV": a}} })

	// Unknown token is rejected.
	if code, _ := req(t, "GET", srv.base+"/deadbeef/kv/KV/secret", ""); code != 403 {
		t.Errorf("unknown token = %d, want 403", code)
	}
	// Valid token reaches only its own scope.
	if code, body := req(t, "GET", srv.URLFor(tokenA)+"/kv/KV/secret", ""); code != 200 || body != "A-data" {
		t.Errorf("token A = %d %q, want 200 A-data", code, body)
	}
	// After Remove, the token no longer resolves.
	srv.Registry().Remove(tokenA)
	if code, _ := req(t, "GET", srv.URLFor(tokenA)+"/kv/KV/secret", ""); code != 403 {
		t.Errorf("removed token = %d, want 403", code)
	}
}

func TestBindingServerMissingBinding(t *testing.T) {
	srv, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	// Undeclared binding name -> 404; a declared-but-nil binding -> 501.
	token, _ := srv.Registry().Add(func() *Scope {
		return &Scope{KV: map[string]KV{"KV": newMemKV()}, Storage: map[string]Storage{"NIL": nil}}
	})
	if code, _ := req(t, "GET", srv.URLFor(token)+"/storage/UNDECLARED/x", ""); code != 404 {
		t.Errorf("undeclared storage binding = %d, want 404", code)
	}
	if code, _ := req(t, "GET", srv.URLFor(token)+"/storage/NIL/x", ""); code != 501 {
		t.Errorf("nil storage binding = %d, want 501", code)
	}
}

func req(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	var r *http.Request
	var err error
	if body != "" {
		r, err = http.NewRequest(method, url, strings.NewReader(body))
	} else {
		r, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
