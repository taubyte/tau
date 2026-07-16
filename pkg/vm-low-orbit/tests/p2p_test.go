package tests

import (
	"context"
	"net/http/httptest"
	"testing"
)

// The p2p guest (HTTP path) opens a stream, builds a command, sends a body, and
// writes the command's response back to HTTP. We assert both directions of the
// guest<->host command round-trip: the body the guest sent reached the mock, and
// the mock's reply reached the HTTP response.
func TestP2PSendCommand(t *testing.T) {
	req := httptest.NewRequest("GET", "/p2p", nil)
	w, code := guestCall(t, context.Background(), "p2p", "callp2p", req, testCtxOpts()...)
	if code != 0 {
		t.Fatalf("guest returned %d, want 0", code)
	}

	if got := w.Body.String(); got != string(p2pReply) {
		t.Fatalf("http body = %q, want the command reply %q", got, p2pReply)
	}

	p2pMock.mu.Lock()
	sent := string(p2pMock.lastBody)
	p2pMock.mu.Unlock()
	if want := `{"something_sent":"Hello, world!"`; len(sent) == 0 || sent[:len(want)] != want {
		t.Fatalf("command body reaching host = %q, want it to start with %q", sent, want)
	}
}
