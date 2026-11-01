package forge

import (
	"net"
	"net/http"
	"strings"
)

// PinnedTransport attaches a credential to requests for one host and nothing
// else. Every forge SDK follows redirects, so the per-hop host check lives here,
// once; it also refuses cleartext http to anything but loopback. The driver
// supplies attach, the header form, but not when it runs. host is a repokey
// host spelling; a request spelling the default port out names the same server.
func PinnedTransport(host string, base http.RoundTripper, attach func(http.Header)) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &pinned{host: host, base: base, attach: attach}
}

type pinned struct {
	host   string
	base   http.RoundTripper
	attach func(http.Header)
}

func (p *pinned) RoundTrip(req *http.Request) (*http.Response, error) {
	to := canonicalHost(req.URL.Scheme, req.URL.Host)
	if to != p.host {
		return nil, &CredentialLeakError{Bound: p.host, To: req.URL.Host}
	}
	if req.URL.Scheme != "https" && !isLoopback(req.URL.Hostname()) {
		return nil, &CredentialLeakError{Bound: p.host, To: req.URL.Scheme + "://" + req.URL.Host}
	}
	// Clone rather than mutate: the caller's request may be retried, and a
	// header this transport wrote would then travel on a hop it did not check.
	clone := req.Clone(req.Context())
	if p.attach != nil {
		p.attach(clone.Header)
	}
	return p.base.RoundTrip(clone)
}

// canonicalHost drops a port that is the scheme's default, which is repokey's
// own rule, so "github.com:443" and "github.com" are recognised as one host.
func canonicalHost(scheme, host string) string {
	h, port, err := net.SplitHostPort(host)
	if err != nil {
		return strings.ToLower(host)
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		if strings.Contains(h, ":") {
			return "[" + strings.ToLower(h) + "]"
		}
		return strings.ToLower(h)
	}
	return strings.ToLower(host)
}

func isLoopback(hostname string) bool {
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(hostname, "[]"))
	return ip != nil && ip.IsLoopback()
}
