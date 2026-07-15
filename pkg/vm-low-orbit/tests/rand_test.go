package tests

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
)

func TestRand(t *testing.T) {
	ctx := context.Background()
	if err := plugins.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/ping", nil)
	w, ret := guestCall(t, ctx, "rand", "randtest", req, testCtxOpts()...)

	// The randtest export returns 1 on success (non-zero)
	if ret != 1 {
		t.Fatalf("randtest returned %d, expected 1 (body: %s)", ret, w.Body.String())
	}

	// Check that the response status is 200
	if w.Code != 200 {
		t.Errorf("response status code = %d, expected 200", w.Code)
	}

	// Check that the response body contains the success message
	body := w.Body.String()
	if !strings.Contains(body, "All buffers are random") {
		t.Errorf("response body missing expected message\n  got: %s", body)
	}
}
