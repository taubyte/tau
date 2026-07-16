//go:build web3

package tests

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
)

// The ipfs guest drives the content host ABI (create/write/seek/read/push/open)
// against the storage mock's content store. storageOpenCid stages the opened
// cid to a file in the working dir, so we run in a temp dir to keep the package
// clean.
func TestIPFS(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })

	req := httptest.NewRequest("GET", "/ipfs", nil)
	w, code := guestCall(t, context.Background(), "ipfs", "someipfs", req, testCtxOpts()...)
	if code != 0 {
		t.Fatalf("guest returned %d: %s", code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"ping": "pong"}` {
		t.Fatalf("body = %q, want success marker", got)
	}
}
