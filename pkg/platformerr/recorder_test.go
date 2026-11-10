package platformerr

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type countingWriter struct {
	*httptest.ResponseRecorder
	headers int
}

func (c *countingWriter) WriteHeader(code int) {
	c.headers++
	c.ResponseRecorder.WriteHeader(code)
}

// A function that writes its body and then sets a status made net/http log
// "superfluous response.WriteHeader call" on every request (about 463 lines
// per node in 30 minutes on the 3-node cloud). Only the first status passes.
func TestLateStatusIsDroppedQuietly(t *testing.T) {
	w := &countingWriter{ResponseRecorder: httptest.NewRecorder()}
	Serve(w, httptest.NewRequest("GET", "/pub", nil), nopLog{}, func(w http.ResponseWriter, _ *http.Request) error {
		w.Write([]byte("ok"))
		w.WriteHeader(500)
		w.WriteHeader(404)
		return nil
	})
	if w.headers != 0 {
		t.Fatalf("late WriteHeader reached the response writer %d times", w.headers)
	}
	if w.Code != 200 || w.Body.String() != "ok" {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
}

func TestFirstStatusStillPasses(t *testing.T) {
	w := &countingWriter{ResponseRecorder: httptest.NewRecorder()}
	Serve(w, httptest.NewRequest("GET", "/", nil), nopLog{}, func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(201)
		w.WriteHeader(500)
		return nil
	})
	if w.headers != 1 || w.Code != 201 {
		t.Fatalf("headers %d, code %d", w.headers, w.Code)
	}
}
