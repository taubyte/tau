package platformerr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatus(t *testing.T) {
	for c, want := range map[Code]int{
		NotFound: 404, NodeBusy: 503, ModuleUnavailable: 502,
		FunctionError: 500, FunctionTimeout: 504, Internal: 500, OverQuota: 429, "": 500, "made_up": 500,
	} {
		if got := Status(c); got != want {
			t.Errorf("%q: got %d want %d", c, got, want)
		}
	}
}

func TestInnermostCodeWins(t *testing.T) {
	err := New(NotFound, errors.New("no match"))
	err = fmt.Errorf("lookup failed with: %w", err)
	if got := New(Internal, err); got != err {
		t.Fatal("New must keep an error that already has a code")
	}
	outer := &Error{Code: Internal, Err: err}
	if c := CodeOf(outer); c != NotFound {
		t.Fatalf("got %q", c)
	}
	if CodeOf(errors.New("plain")) != "" || CodeOf(nil) != "" {
		t.Fatal("uncoded errors have no code")
	}
	if err.Error() != "lookup failed with: no match" {
		t.Fatalf("text changed: %q", err)
	}
}

func TestWriteNeverShowsErrText(t *testing.T) {
	for _, c := range []struct {
		err   error
		code  Code
		retry string
	}{
		{New(NotFound, errors.New("secret-cid /project/x")), NotFound, ""},
		{New(NodeBusy, errors.New("secret-cid")), NodeBusy, "1"},
		{errors.New("secret-cid"), Internal, ""},
		{&Error{Code: "made_up", Err: errors.New("secret-cid")}, Internal, ""},
	} {
		w := httptest.NewRecorder()
		Write(w, c.err, "req-1")
		if strings.Contains(w.Body.String(), "secret-cid") {
			t.Fatalf("leaked: %s", w.Body)
		}
		var body struct {
			Code      int    `json:"code"`
			Error     Code   `json:"error"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != Status(c.code) || body.Code != w.Code || body.Error != c.code || body.Message == "" || body.RequestID != "req-1" {
			t.Fatalf("%d %+v", w.Code, body)
		}
		if w.Header().Get("Retry-After") != c.retry {
			t.Fatalf("%q retry %q", c.code, w.Header().Get("Retry-After"))
		}
	}
}

type nopLog struct{}

func (nopLog) Debugf(string, ...any) {}
func (nopLog) Errorf(string, ...any) {}

// Once the response has started, an error body would corrupt it, and a clean
// end would pass a short body off as whole: the connection is aborted instead.
// This test used to expect Serve to return normally.
func TestServeLeavesStartedResponse(t *testing.T) {
	w := httptest.NewRecorder()
	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", rec)
		}
		if w.Code != 200 || w.Body.String() != "hi" {
			t.Fatalf("%d %q", w.Code, w.Body)
		}
	}()
	Serve(w, httptest.NewRequest("GET", "/", nil).WithContext(context.WithValue(context.Background(), http.ServerContextKey, &http.Server{})), nopLog{}, func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(200)
		w.Write([]byte("hi"))
		return errors.New("secret-cid")
	})
	t.Fatal("Serve returned normally")
}

func TestServeWritesErrorBeforeResponseStarts(t *testing.T) {
	w := httptest.NewRecorder()
	Serve(w, httptest.NewRequest("GET", "/", nil), nopLog{}, func(http.ResponseWriter, *http.Request) error { return errors.New("secret-cid") })
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret-cid") {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
}

// Substrate serves tunneled requests on libp2p goroutines, where nothing
// recovers the abort panic: it took down whole nodes.
func TestServeDoesNotPanicOutsideNetHTTP(t *testing.T) {
	w := httptest.NewRecorder()
	Serve(w, httptest.NewRequest("GET", "/", nil), nopLog{}, func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("hi"))
		return errors.New("broke")
	})
	if w.Code != 200 || w.Body.String() != "hi" {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
}
