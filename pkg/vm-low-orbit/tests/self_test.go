package tests

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
	vmContext "github.com/taubyte/tau/pkg/vm/context"
)

func TestSelf(t *testing.T) {
	ctx := context.Background()
	// self has no backend node; initialize the plugin with no options.
	if err := plugins.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/self", nil)
	w, ret := guestCall(t, ctx, "self", "selftest", req,
		vmContext.Project("proj-123"),
		vmContext.Application("app-456"),
		vmContext.Resource("res-789"),
		vmContext.Commit("commit-abc"),
		vmContext.Branch("master"),
	)

	if ret != 0 {
		t.Fatalf("selftest returned %d (body: %s)", ret, w.Body.String())
	}

	body := w.Body.String()
	for _, want := range []string{"res-789", "proj-123", "app-456", "commit-abc", "master"} {
		if !strings.Contains(body, want) {
			t.Errorf("response missing %q\nbody: %s", want, body)
		}
	}
}
