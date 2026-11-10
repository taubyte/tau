package event

import (
	"context"
	"encoding/binary"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
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
	f.ReleaseEvent(id)
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

type memMod struct {
	vm.Module
	buf []byte
}

type mem struct {
	vm.Memory
	b []byte
}

func (m *memMod) Memory() vm.Memory { return &mem{b: m.buf} }
func (m *mem) Read(o, c uint32) ([]byte, bool) {
	if uint64(o)+uint64(c) > uint64(len(m.b)) {
		return nil, false
	}
	return m.b[o : o+c], true
}
func (m *mem) WriteUint32Le(o, v uint32) bool { binary.LittleEndian.PutUint32(m.b[o:], v); return true }

// write puts body in guest memory (after 4 bytes for the count) and calls eventHttpWrite.
func write(f *Factory, id uint32, body string) uint32 {
	mod := &memMod{buf: make([]byte, 4+len(body))}
	copy(mod.buf[4:], body)
	return f.eventHttpWrite(context.Background(), mod, id, 4, uint32(len(body)), 0)
}

func newF() (*Factory, uint32, *httptest.ResponseRecorder) {
	f, id, rec := newHTTPEvent()
	f.Methods = helpers.New(context.Background())
	return f, id, rec
}

func TestWriteThenReturnKeepsStatus(t *testing.T) {
	f, id, rec := newF()
	if rc := write(f, id, "nope"); rc != 0 {
		t.Fatal(rc)
	}
	f.eventHttpRetCode(context.Background(), nil, id, 404)
	f.ReleaseEvent(id)
	if rec.Code != 404 || rec.Body.String() != "nope" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestFlushThenReturnStays200(t *testing.T) {
	f, id, rec := newF()
	write(f, id, "hi")
	f.eventHttpFlush(context.Background(), nil, id)
	if rc := f.eventHttpRetCode(context.Background(), nil, id, 404); rc == 0 {
		t.Fatal("Return after flush should fail")
	}
	f.ReleaseEvent(id)
	if rec.Code != 200 || rec.Body.String() != "hi" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestReturnOnlyCommitsOnRelease(t *testing.T) {
	f, id, rec := newF()
	f.eventHttpRetCode(context.Background(), nil, id, 404)
	f.ReleaseEvent(id)
	f.ReleaseEvent(id)
	if rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestOverCapStreams(t *testing.T) {
	f, id, rec := newF()
	f.eventHttpRetCode(context.Background(), nil, id, 202)
	write(f, id, strings.Repeat("x", maxBuffered))
	write(f, id, "tail")
	f.ReleaseEvent(id)
	if rec.Code != 202 || rec.Body.Len() != maxBuffered+4 {
		t.Fatalf("got %d len %d", rec.Code, rec.Body.Len())
	}
}

func TestHeaderSetReplacesAndDelDeletes(t *testing.T) {
	f, id, rec := newF()
	mod := &memMod{buf: []byte("kAvalBval")} // key 0:1, Aval 1:4, Bval 5:4
	ctx := context.Background()
	if rc := f.eventHttpHeaderAdd(ctx, mod, id, 0, 1, 1, 4); rc != 0 { // k: Aval
		t.Fatal(rc)
	}
	if rc := f.eventHttpHeaderSet(ctx, mod, id, 0, 1, 5, 4); rc != 0 { // k: Bval
		t.Fatal(rc)
	}
	if got := rec.Header().Values("K"); len(got) != 1 || got[0] != "Bval" {
		t.Fatalf("Set did not replace: %v", got)
	}
	if rc := f.eventHttpHeaderDel(ctx, mod, id, 0, 1); rc != 0 {
		t.Fatal(rc)
	}
	if got := rec.Header().Values("K"); len(got) != 0 {
		t.Fatalf("Del left %v", got)
	}
}
