package tests

import (
	"context"
	"net/http/httptest"
	"testing"
)

// TestDatabase exercises the database plugin (New/Put/Get/Delete/List) end to
// end against the real size-tracking KV backed by the in-memory kvdb mock.
func TestDatabase(t *testing.T) {
	req := httptest.NewRequest("GET", "/db", nil)
	w, ret := guestCall(t, context.Background(), "database", "databasetest", req, testCtxOpts()...)

	if ret != 0 {
		t.Fatalf("databasetest returned %d (body: %s)", ret, w.Body.String())
	}
	// the guest writes back the value it stored and retrieved.
	if got := w.Body.String(); got != "DatabaseTest" {
		t.Fatalf("body = %q, want %q", got, "DatabaseTest")
	}
}
