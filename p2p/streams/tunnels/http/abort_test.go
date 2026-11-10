package httptun

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taubyte/tau/pkg/platformerr"
)

type nopLog struct{}

func (nopLog) Debugf(string, ...any) {}
func (nopLog) Errorf(string, ...any) {}

// A function that fails after it began writing, on the backend's stream
// goroutine: the frontend must fail too, not end with a clean short response.
func TestFailureAfterResponseBeganReachesFrontend(t *testing.T) {
	front, back := net.Pipe()
	go func() {
		defer back.Close()
		w, r, err := Backend(back)
		if err != nil {
			t.Error(err)
			return
		}
		platformerr.Serve(w, r, nopLog{}, func(w http.ResponseWriter, r *http.Request) error {
			w.Write([]byte("partial"))
			return errors.New("trap")
		})
	}()

	rec := httptest.NewRecorder()
	if err := waitFrontend(t, frontendAsync(rec, httptest.NewRequest("GET", "/", nil), front)); err == nil {
		t.Fatalf("a broken response must fail the frontend, client got %d %q", rec.Code, rec.Body.String())
	}
}
