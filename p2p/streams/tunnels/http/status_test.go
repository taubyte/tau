package httptun

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

func encodeHeaders(t *testing.T, code int32) []byte {
	t.Helper()
	b, err := cbor.Marshal(headersOpPayload{Headers: http.Header{}, Code: code})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The response code in a headers frame is attacker-controlled (a peer, or a
// substrate forwarding a guest's code). net/http's WriteHeader panics for a
// code outside 100..999, and this runs on the Frontend goroutine with no
// net/http recover — so headersOp must clamp it rather than crash the process.
func TestHeadersOpClampsOutOfRangeCode(t *testing.T) {
	for _, code := range []int32{0, 99, 1000, -1, 2000000} {
		rec := httptest.NewRecorder()
		if err := headersOp(rec, bytes.NewReader(encodeHeaders(t, code))); err != nil {
			t.Fatalf("headersOp(code=%d) errored: %v", code, err)
		}
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("headersOp(code=%d) wrote status %d; want it clamped to 500", code, rec.Code)
		}
	}
}

func TestHeadersOpPassesValidCode(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := headersOp(rec, bytes.NewReader(encodeHeaders(t, 404))); err != nil {
		t.Fatalf("headersOp(404) errored: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
