package httptun

import (
	"errors"
	"io"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

// brokenStream refuses writes, so sending the request fails, and its reads end.
type brokenStream struct{}

func (brokenStream) Read([]byte) (int, error)  { return 0, io.EOF }
func (brokenStream) Write([]byte) (int, error) { return 0, errors.New("stream reset") }

// When the request cannot be sent, Frontend returns without reading the reader
// goroutine's result; that goroutine must still exit.
func TestFrontendSendFailureLeaksNoGoroutine(t *testing.T) {
	before := runtime.NumGoroutine()
	for range 50 {
		if err := Frontend(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), brokenStream{}); err == nil {
			t.Fatal("want a send error")
		}
	}
	for deadline := time.Now().Add(2 * time.Second); runtime.NumGoroutine() > before; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines: %d before, %d after", before, runtime.NumGoroutine())
		}
	}
}
