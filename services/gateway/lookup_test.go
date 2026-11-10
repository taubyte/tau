package gateway

import (
	"errors"
	"fmt"
	goHttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/taubyte/tau/pkg/platformerr"
)

func TestNoMatchSaysWhatHappened(t *testing.T) {
	for _, c := range []struct {
		name   string
		result lookupResult
		status int
		retry  string
	}{
		{"every substrate said no", lookupResult{notFound: true}, 404, ""},
		{"one said no, one failed", lookupResult{notFound: true, failed: true}, 503, "1"},
		{"all failed", lookupResult{failed: true}, 503, "1"},
		{"nobody reachable", lookupResult{}, 503, "1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			platformerr.Write(w, noMatch(&c.result), "")
			if w.Code != c.status || w.Header().Get("Retry-After") != c.retry {
				t.Fatalf("status %d retry %q", w.Code, w.Header().Get("Retry-After"))
			}
		})
	}
}

func TestOtherErrorsStay500(t *testing.T) {
	w := httptest.NewRecorder()
	platformerr.Write(w, errors.New("tunneling failed"), "")
	if w.Code != goHttp.StatusInternalServerError {
		t.Fatalf("status %d", w.Code)
	}
}

func TestDefiniteStopsRetrying(t *testing.T) {
	if !(&lookupResult{notFound: true}).definite() {
		t.Fatal("a clear no is final")
	}
	for _, r := range []*lookupResult{{}, {failed: true}, {notFound: true, failed: true}} {
		if r.definite() {
			t.Fatalf("%+v must be asked again", r)
		}
	}
}

func TestOnlyNoMatchIsNotFound(t *testing.T) {
	for msg, want := range map[string]bool{
		"lookup failed with: no HTTP match found for method `GET` on `https://a.b/x`": true,
		"lookup failed with: creating new tns path failed with: i/o timeout":          false,
		"lookup failed with: validating dns failed":                                   false,
		"lookup failed with: no HTTP match found ... context deadline exceeded":       false,
	} {
		err := errors.New(msg)
		if got := notFound(err); got != want {
			t.Errorf("%q: got %v", msg, got)
		}
	}
}

// The user's host and path are in the error text; they must not decide anything.
func TestNotFoundWhenPathSaysTimeout(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("peer p returned error for command \"proxy\": %w", platformerr.New(platformerr.NotFound,
			errors.New("no HTTP match found for method `GET` on `https://deadline.example.com/api/timeout`"))),
		errors.New("lookup failed with: no HTTP match found for method `GET` on `https://a.b/api/timeout`"), // old substrate, no code
	} {
		nf := notFound(err)
		w := httptest.NewRecorder()
		platformerr.Write(w, noMatch(&lookupResult{notFound: nf, failed: !nf}), "")
		if w.Code != goHttp.StatusNotFound {
			t.Fatalf("%v: status %d", err, w.Code)
		}
	}
}

func TestRewordedNotFoundIsStillNotFound(t *testing.T) {
	err := fmt.Errorf("peer p returned error for command \"proxy\": %w", platformerr.New(platformerr.NotFound, errors.New("nothing is served here")))
	if !notFound(err) {
		t.Fatal("a coded not-found is not found whatever its text")
	}
}
