package forgetest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// decoy is a host the suite OWNS and no credential is for. It answers the one
// question a fixture cannot answer about itself: a fixture records the requests
// that ARRIVED at it, so the request that went somewhere else is the one it
// never sees. This is somewhere else, and it keeps a list.
//
// IT IS CLEARTEXT ON LOOPBACK, deliberately. A TLS decoy would be refused at
// the handshake by a driver's own transport, which trusts one certificate, and
// a refusal at the handshake is indistinguishable from a refusal at the
// credential pin — the check would pass for the wrong reason and no one would
// know. Loopback also keeps forge.PinnedTransport's cleartext rule out of it,
// so the thing that must refuse this request is the HOST check and nothing
// standing in front of it.
//
// It answers 204 to everything, which is the friendliest possible reply: a
// driver that follows the redirect gets something that parses as nothing rather
// than an error it can blame on the network.
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

// host is the authority half, which is what a failure message names.
func (d *decoy) host() string { return strings.TrimPrefix(d.srv.URL, "http://") }

func (d *decoy) seen() []Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Request(nil), d.arrivals...)
}

func (d *decoy) close() { d.srv.Close() }
