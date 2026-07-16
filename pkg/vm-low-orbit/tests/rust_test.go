package tests

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// Rust guest counterparts. The rust-sdk imports the same "taubyte/sdk" host
// functions, so these exercise the same plugins from a different language.

func TestDatabaseRust(t *testing.T) {
	req := httptest.NewRequest("GET", "/db", nil)
	w, _ := guestCall(t, context.Background(), "database_rs", "databasetest", req, testCtxOpts()...)
	if got := w.Body.String(); !strings.Contains(got, "DatabaseTest") {
		t.Fatalf("body = %q, want it to contain %q", got, "DatabaseTest")
	}
}
