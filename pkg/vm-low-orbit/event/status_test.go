package event

import (
	"context"
	"net/http/httptest"
	"testing"
)

// newHTTPEvent registers an HTTP event backed by a recording ResponseWriter.
// httptest's WriteHeader panics on an out-of-range code (like a real server),
// so an unclamped code reaching it fails these tests loudly.
func newHTTPEvent() (*Factory, uint32, *httptest.ResponseRecorder) {
	f := &Factory{events: make(map[uint32]*Event)}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	e := f.CreateHttpEvent(rec, req)
	return f, e.Id, rec
}

// A guest status code outside net/http's valid 100..999 range must be rejected
// before it reaches WriteHeader — otherwise it panics the gateway's response
// goroutine. (module is unused by retcode; the code check runs first.)
func TestEventHttpRetCodeRejectsOutOfRange(t *testing.T) {
	for _, code := range []uint32{0, 99, 1000, 0xFFFFFFFF} {
		f, id, rec := newHTTPEvent()
		rc := f.eventHttpRetCode(context.Background(), nil, id, code)
		if rc == 0 {
			t.Errorf("eventHttpRetCode(code=%d) accepted an out-of-range code", code)
		}
		if rec.Code != 200 {
			t.Errorf("eventHttpRetCode(code=%d) wrote status %d; the writer should be untouched", code, rec.Code)
		}
	}
}

func TestEventHttpRetCodeAcceptsValid(t *testing.T) {
	f, id, rec := newHTTPEvent()
	if rc := f.eventHttpRetCode(context.Background(), nil, id, 418); rc != 0 {
		t.Fatalf("eventHttpRetCode(418) = errno %d, want 0", rc)
	}
	if rec.Code != 418 {
		t.Fatalf("status = %d, want 418", rec.Code)
	}
}

// http.Redirect calls WriteHeader(code) too; the code is validated before the
// url is read, so a nil module is fine here.
func TestEventHttpRedirectRejectsOutOfRange(t *testing.T) {
	f, id, rec := newHTTPEvent()
	rc := f.eventHttpRedirect(context.Background(), nil, id, 0, 0, 0)
	if rc == 0 {
		t.Fatalf("eventHttpRedirect(code=0) accepted an out-of-range code")
	}
	if rec.Code != 200 {
		t.Fatalf("eventHttpRedirect(code=0) wrote status %d; the writer should be untouched", rec.Code)
	}
}
