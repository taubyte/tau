package forgetest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// decoy is a host the suite owns and no credential is for: a fixture sees only
// the requests that arrived at it, and this is somewhere else, keeping a list.
// It is cleartext on loopback so the host check, not a TLS handshake, is what
// must refuse; it answers 204, which parses as nothing.
type decoy struct {
	srv *httptest.Server

	mu       sync.Mutex
	arrivals []Request
}

func newDecoy() *decoy {
	d := &decoy{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.arrivals = append(d.arrivals, Request{
			Method: r.Method,
			URL:    "http://" + r.Host + r.URL.RequestURI(),
			Header: r.Header.Clone(),
		})
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	return d
}

// url is where the forge is told to send the driver. The path is named so that
// a request showing up in somebody's log is self-explaining.
func (d *decoy) url() string { return d.srv.URL + "/not-the-forge" }

func (d *decoy) host() string { return strings.TrimPrefix(d.srv.URL, "http://") }

func (d *decoy) seen() []Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Request(nil), d.arrivals...)
}

func (d *decoy) close() { d.srv.Close() }
