package tests

import (
	"context"
	"net/http/httptest"
	"testing"

	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
)

func TestRedirect(t *testing.T) {
	ctx := context.Background()
	if err := plugins.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/test", nil)
	w, ret := guestCall(t, ctx, "redirect", "redirecttest", req, testCtxOpts()...)

	if ret != 0 {
		t.Fatalf("redirecttest returned %d (body: %s)", ret, w.Body.String())
	}

	// Redirect should set status to 307 (Temporary)
	if w.Code != 307 {
		t.Errorf("Expected status code 307, got %d", w.Code)
	}

	// Location header should be set to the redirect URL
	location := w.Header().Get("Location")
	expectedLocation := "https://p2p.skelouse.com/ping"
	if location != expectedLocation {
		t.Errorf("Expected Location header %q, got %q", expectedLocation, location)
	}
}
