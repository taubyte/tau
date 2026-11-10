package httptun

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// roundTrip plays the substrate side into a buffer, then has Frontend replay
// it onto a gateway response that already carries a gateway-owned header.
func roundTrip(t *testing.T, substrate func(w http.ResponseWriter)) *httptest.ResponseRecorder {
	t.Helper()
	var frames bytes.Buffer
	substrate(newResponseWriter(&struct {
		io.Reader
		io.Writer
	}{nil, &frames}))

	rec := httptest.NewRecorder()
	rec.Header().Set("X-Substrate-Peer", "gw")
	req := httptest.NewRequest("GET", "http://x/", nil)
	if err := Frontend(rec, req, &struct {
		io.Reader
		io.Writer
	}{&frames, io.Discard}); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestHeadersSurviveTunnel(t *testing.T) {
	for name, tc := range map[string]struct {
		code int
		fn   func(w http.ResponseWriter)
	}{
		"status then body": {404, func(w http.ResponseWriter) {
			w.Header().Set("X-Fn", "1")
			w.WriteHeader(404)
			w.Write([]byte("X"))
		}},
		"body without status": {200, func(w http.ResponseWriter) {
			w.Header().Set("X-Fn", "1")
			w.Write([]byte("X"))
			w.WriteHeader(200) // the substrate's closing call
		}},
	} {
		rec := roundTrip(t, tc.fn)
		if rec.Code != tc.code || rec.Body.String() != "X" {
			t.Errorf("%s: got %d %q", name, rec.Code, rec.Body.String())
		}
		if rec.Result().Header.Get("X-Substrate-Peer") != "gw" {
			t.Errorf("%s: gateway header lost", name)
		}
		if rec.Result().Header.Get("X-Fn") != "1" {
			t.Errorf("%s: function header lost", name)
		}
	}
}

func TestFunctionOverridesGatewayHeader(t *testing.T) {
	rec := roundTrip(t, func(w http.ResponseWriter) {
		w.Header().Set("X-Substrate-Peer", "fn")
		w.WriteHeader(200)
	})
	if got := rec.Result().Header.Get("X-Substrate-Peer"); got != "fn" {
		t.Fatalf("got %q, want the function's value", got)
	}
}
