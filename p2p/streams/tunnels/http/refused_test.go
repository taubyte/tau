package httptun

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

type wrapper struct{ http.ResponseWriter }

func (w wrapper) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// platformerr.Serve hands the handler a wrapper; the mark must still reach the tunnel.
func TestRefuseReachesTunnelThroughWrappers(t *testing.T) {
	var stream bytes.Buffer
	rw := &responseWriter{header: http.Header{}, stream: &stream}
	rw.pack = newResponseWriter(&nopReadWriter{&stream}).(*responseWriter).pack
	Refuse(wrapper{wrapper{rw}})
	if !rw.refused {
		t.Fatal("not marked")
	}
	Refuse(httptest.NewRecorder()) // no tunnel: must not panic
}

type nopReadWriter struct{ *bytes.Buffer }

func refusedHeaders(t *testing.T) []byte {
	t.Helper()
	b, err := cbor.Marshal(headersOpPayload{Headers: http.Header{"X": {"y"}}, Code: 503, Refused: true})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRefusalLeavesClientUntouchedWhenReplayable(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := headersOp(rec, bytes.NewReader(refusedHeaders(t)), true); !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v", err)
	}
	if rec.Flushed || rec.Header().Get("X") != "" {
		t.Fatal("wrote to the client")
	}
}

func TestRefusalIsPassedOnWhenNotReplayable(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := headersOp(rec, bytes.NewReader(refusedHeaders(t)), false); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 503 {
		t.Fatalf("status %d", rec.Code)
	}
}

// An old gateway reads a new frame: the extra key must be ignored, not an error.
func TestRefusedFrameDecodesOnOldFrontend(t *testing.T) {
	var old struct {
		Headers http.Header `cbor:"1,keyasint"`
		Code    int32       `cbor:"2,keyasint"`
	}
	if err := cbor.Unmarshal(refusedHeaders(t), &old); err != nil {
		t.Fatal(err)
	}
	if old.Code != 503 {
		t.Fatalf("code %d", old.Code)
	}
}
