package tests

import (
	"context"
	"net/http/httptest"
	"testing"
)

// TestGlobals exercises the globals plugin (u32 + string globals via the global
// database), backed by the DB mock.
func TestGlobals(t *testing.T) {
	req := httptest.NewRequest("GET", "/g", nil)
	w, ret := guestCall(t, context.Background(), "globals", "globaltest", req, testCtxOpts()...)
	if ret != 0 {
		t.Fatalf("globaltest returned %d (body: %s)", ret, w.Body.String())
	}
}
