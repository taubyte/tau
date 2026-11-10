package httptun

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taubyte/tau/p2p/streams/packer"
)

// tunnelStream behaves like a tunnel whose backend stopped reading: writes pass
// up to window bytes and then block until the stream is closed. Reads return
// response and then either EOF, or block until the stream is closed.
type tunnelStream struct {
	response io.Reader
	hold     bool // block after response instead of EOF
	window   int

	mu      sync.Mutex
	written int
	closed  chan struct{}
	once    sync.Once
}

func newTunnelStream(response io.Reader, window int, hold bool) *tunnelStream {
	return &tunnelStream{response: response, window: window, hold: hold, closed: make(chan struct{})}
}

func (s *tunnelStream) Close() { s.once.Do(func() { close(s.closed) }) }

func (s *tunnelStream) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

func (s *tunnelStream) Read(p []byte) (int, error) {
	n, err := s.response.Read(p)
	if n > 0 || !s.hold {
		return n, err
	}
	<-s.closed
	return 0, errors.New("stream reset")
}

func (s *tunnelStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	full := s.written+len(p) > s.window
	if !full {
		s.written += len(p)
	}
	s.mu.Unlock()
	if full {
		<-s.closed
		return 0, errors.New("stream reset")
	}
	return len(p), nil
}

func frontendAsync(w http.ResponseWriter, r *http.Request, s io.ReadWriter) <-chan error {
	ret := make(chan error, 1)
	go func() { ret <- Frontend(w, r, s) }()
	return ret
}

func waitFrontend(t *testing.T, ret <-chan error) error {
	t.Helper()
	select {
	case err := <-ret:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Frontend did not return")
		return nil
	}
}

func encodedResponse(code int, header http.Header, body string) *bytes.Buffer {
	var buf bytes.Buffer
	w := newResponseWriter(&buf)
	for k, v := range header {
		w.Header()[k] = v
	}
	w.WriteHeader(code)
	if body != "" {
		w.Write([]byte(body))
	}
	return &buf
}

// A POST larger than the stream window, to a backend that answers without
// reading it, must get the answer and leave no goroutine or stream behind.
func TestFrontendUnreadUploadReturnsAndLeaksNothing(t *testing.T) {
	before := runtime.NumGoroutine()
	for range 20 {
		s := newTunnelStream(encodedResponse(http.StatusServiceUnavailable, nil, "busy"), 64<<10, false)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/", bytes.NewReader(make([]byte, 8<<20)))

		if err := waitFrontend(t, frontendAsync(rec, req, s)); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "busy" {
			t.Fatalf("status %d body %q", rec.Code, rec.Body)
		}
		if !s.isClosed() {
			t.Fatal("stream left open")
		}
	}
	for deadline := time.Now().Add(2 * time.Second); runtime.NumGoroutine() > before; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines: %d before, %d after", before, runtime.NumGoroutine())
		}
	}
}

// A refused replayable request still reports ErrRefused, with nothing written,
// so the gateway can move it to another node.
func TestFrontendRefusalStillReported(t *testing.T) {
	var buf bytes.Buffer
	w := newResponseWriter(&buf)
	Refuse(w)
	w.WriteHeader(http.StatusServiceUnavailable)

	s := newTunnelStream(&buf, 64<<10, false)
	rec := httptest.NewRecorder()
	err := waitFrontend(t, frontendAsync(rec, httptest.NewRequest("GET", "/", nil), s))
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v", err)
	}
	if rec.Flushed || rec.Body.Len() != 0 {
		t.Fatalf("response touched: flushed=%v body=%q", rec.Flushed, rec.Body)
	}
}

func TestFrontendClientGoneTearsDownTunnel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newTunnelStream(strings.NewReader(""), 64<<10, true)
	ret := frontendAsync(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(ctx), s)

	time.Sleep(50 * time.Millisecond)
	select {
	case err := <-ret:
		t.Fatalf("returned before the client left: %v", err)
	default:
	}

	cancel()
	if err := waitFrontend(t, ret); err != nil {
		t.Fatalf("a client that left is not a failure, got %v", err)
	}
	if !s.isClosed() {
		t.Fatal("stream left open")
	}
}

func TestFrontendFlushesOnlyUnknownLengthBodies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		header  http.Header
		flushed bool
	}{
		{"unknown length", nil, true},
		{"declared length", http.Header{"Content-Length": {"2"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTunnelStream(encodedResponse(http.StatusOK, tc.header, "ok"), 64<<10, false)
			rec := httptest.NewRecorder()
			if err := waitFrontend(t, frontendAsync(rec, httptest.NewRequest("GET", "/", nil), s)); err != nil {
				t.Fatal(err)
			}
			if rec.Body.String() != "ok" || rec.Flushed != tc.flushed {
				t.Fatalf("body %q flushed %v, want flushed %v", rec.Body, rec.Flushed, tc.flushed)
			}
		})
	}
}

// Guests may read until EOF more than once.
func TestBodyReaderReadAfterEOF(t *testing.T) {
	var buf bytes.Buffer
	pack := packer.New(Magic, Version)
	pack.Stream(BodyOp, &buf, strings.NewReader(""), 4096)
	b := newBodyReader(pack, BodyOp, &buf)

	p := make([]byte, 16)
	for range 2 {
		if n, err := b.Read(p); n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("n=%d err=%v", n, err)
		}
	}
}

// delayedReader holds its response back so the upload is already blocked on the client.
type delayedReader struct {
	io.Reader
	once sync.Once
}

func (d *delayedReader) Read(p []byte) (int, error) {
	d.once.Do(func() { time.Sleep(100 * time.Millisecond) })
	return d.Reader.Read(p)
}

// A client that stops mid-upload still gets the answer: net/http must not make
// the response wait for the body read that is blocked on that client.
func TestFrontendAnswersClientStalledMidUpload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Frontend(w, r, newTunnelStream(&delayedReader{Reader: encodedResponse(http.StatusServiceUnavailable, nil, "busy")}, 64<<10, false))
	}))
	defer srv.CloseClientConnections()

	c, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = io.WriteString(c, "POST / HTTP/1.1\r\nHost: a\r\nContent-Length: 1048576\r\n\r\npartial"); err != nil {
		t.Fatal(err)
	}

	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	res, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("no answer while the upload is stalled: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", res.StatusCode)
	}
}
