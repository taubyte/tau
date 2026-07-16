package tests

import (
	"context"
	"net/http/httptest"
	"testing"
)

// The storage guest drives the full file host ABI (add/read/list/cid/delete)
// against the in-memory storage mock. A 0 return means every round-trip
// assertion inside the guest held; a non-zero return writes the failing step to
// the body.
func TestStorage(t *testing.T) {
	req := httptest.NewRequest("GET", "/storage", nil)
	w, code := guestCall(t, context.Background(), "storage", "storagetest", req, testCtxOpts()...)
	if code != 0 {
		t.Fatalf("guest returned %d: %s", code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"ping": "pong"}` {
		t.Fatalf("body = %q, want success marker", got)
	}
}
