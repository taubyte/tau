package platformerr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatus(t *testing.T) {
	for c, want := range map[Code]int{
		NotFound: 404, NodeBusy: 503, ModuleUnavailable: 502,
		FunctionError: 500, FunctionTimeout: 504, Internal: 500, "": 500, "made_up": 500,
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
