package tests

import (
	"context"
	"net/http/httptest"
	"testing"

	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
)

func TestJson(t *testing.T) {
	ctx := context.Background()
	if err := plugins.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/test", nil)
	w, ret := guestCall(t, ctx, "json", "jsontest", req, testCtxOpts()...)

	if ret != 0 {
		t.Fatalf("jsontest returned %d (body: %s)", ret, w.Body.String())
	}

	body := w.Body.String()
	expectedBody := `{"UUID":"ewefwefwe","State":"TX","Titus":{"Ti1":{"UUID":"qwdqwdqw","State":"","Titus":null}}}`
	if body != expectedBody {
		t.Errorf("Got body %q, expected %q", body, expectedBody)
	}
}
