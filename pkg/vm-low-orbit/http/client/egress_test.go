package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A guest's HTTP client must not reach node-local services. httptest binds
// 127.0.0.1, which the egress policy denies, so the request must fail at dial.
// On the pre-fix client (&http.Client{}) this request succeeds — this is the
// regression guard.
func TestGuestHTTPClientBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := restrictedHTTPClient().Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("guest client reached loopback server %s; egress guard not applied", srv.URL)
	}
	if !strings.Contains(err.Error(), "not permitted") {
		t.Fatalf("expected a netguard rejection, got: %v", err)
	}
}
