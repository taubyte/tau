package tests

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
)

// TestBasic and TestBasicRust exercise the http/event plugin with no backend:
// the guest reads the request and writes a response.
func TestBasic(t *testing.T) {
	ctx := context.Background()
	if err := plugins.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	w, _ := guestCall(t, ctx, "basic", "basic", req, testCtxOpts()...)

	if got := w.Body.String(); got != "hello world" {
		t.Fatalf("body = %q, want %q", got, "hello world")
	}
}

func TestBasicRust(t *testing.T) {
	ctx := context.Background()
	if err := plugins.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/", strings.NewReader("payload:"))
	w, _ := guestCall(t, ctx, "basic_rs", "do_stuff", req, testCtxOpts()...)

	// do_stuff echoes the request body then appends the marker.
	if got := w.Body.String(); !strings.Contains(got, "Hello, world RUST!") {
		t.Fatalf("body = %q, want it to contain %q", got, "Hello, world RUST!")
	}
}
