package gateway

import (
	"context"
	"errors"
	"fmt"
	goHttp "net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestIsTimeout(t *testing.T) {
	for err, want := range map[error]bool{
		context.DeadlineExceeded:                      true,
		os.ErrDeadlineExceeded:                        true,
		fmt.Errorf("x: %w", context.DeadlineExceeded): true,
		errors.New("read: i/o timeout"):               true,
		errors.New("lookup failed with: no such"):     false,
	} {
		if got := isTimeout(err); got != want {
			t.Errorf("%v: got %v", err, got)
		}
	}
}

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
			writeError(w, noMatch(&c.result))
			if w.Code != c.status || w.Header().Get("Retry-After") != c.retry {
				t.Fatalf("status %d retry %q", w.Code, w.Header().Get("Retry-After"))
			}
		})
	}
}

func TestOtherErrorsStay500(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, errors.New("tunneling failed"))
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
		if got := isNotFound(err) && !isTimeout(err); got != want {
			t.Errorf("%q: got %v", msg, got)
		}
	}
}
