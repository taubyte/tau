package tests

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
)

func TestHttpMethod(t *testing.T) {
	ctx := context.Background()
	if err := plugins.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/test", nil)
	w, ret := guestCall(t, ctx, "http_method", "methodHttp", req, testCtxOpts()...)

	if ret != 0 {
		t.Fatalf("methodHttp returned %d (body: %s)", ret, w.Body.String())
	}

	body := w.Body.String()
	if !strings.Contains(body, "Success") {
		t.Errorf("response missing %q\nbody: %s", "Success", body)
	}
}
