package wasm

import (
	"fmt"
	"net/http"
	"strings"
)

// egress.go is where a guest's outbound HTTP is decided.
//
// # Why there is an HTTP client here at all
//
// A component that exports wasi:http/incoming-handler usually imports
// wasi:http/outgoing-handler beside it — that is what "a function that calls an
// API" is in the standard world. Registering the incoming half without the
// outgoing one would refuse a large share of the components this tier exists to
// be able to run.
//
// # So it is governed rather than absent
//
// The resource says where it may reach (`egress`), and this is the only door.
// A resource that declared none reaches nowhere: EMPTY IS DENY, because a
// resource that declared no egress declared no egress, and reading an absent
// list as "anywhere" would make the safe spelling the one nobody writes.
//
// It is the same rule the container tier states with a firewall, arrived at
// with the only mechanism a wasm guest has — there is no socket to filter,
// because the guest never gets one.

// allowlist is an http.RoundTripper that refuses every host but the ones named.
type allowlist struct {
	hosts map[string]bool
	next  http.RoundTripper
}

// egressFor builds the client wasi:http/outgoing-handler dispatches through.
func egressFor(hosts []string, next http.RoundTripper) *http.Client {
	if next == nil {
		next = http.DefaultTransport
	}

	allowed := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		// LOWERCASED AND WITHOUT A PORT, because a host name is
		// case-insensitive and the author wrote a name rather than an
		// endpoint. `api.stripe.com` allows it on any port it is reached on,
		// which is what the vocabulary's own doc for `egress` describes.
		allowed[strings.ToLower(strings.TrimSpace(host))] = true
	}

	return &http.Client{Transport: &allowlist{hosts: allowed, next: next}}
}

func (a *allowlist) RoundTrip(req *http.Request) (*http.Response, error) {
	host := strings.ToLower(req.URL.Hostname())

	// A WILDCARD IS ONE LABEL AND NEVER THE WHOLE NAME. `*.stripe.com` allows
	// api.stripe.com and does NOT allow stripe.com or a.b.stripe.com — the
	// same rule every certificate follows, so an author's intuition about it is
	// already right.
	if !a.hosts[host] && !a.hosts[wildcard(host)] {
		return nil, fmt.Errorf("wasm: this workload may not reach %s; add it to the resource's egress", host)
	}
	return a.next.RoundTrip(req)
}

// wildcard is a host with its first label replaced by "*", or "" for a name
// with no parent to wildcard.
func wildcard(host string) string {
	label, rest, ok := strings.Cut(host, ".")
	if !ok || label == "" || rest == "" || !strings.Contains(rest, ".") {
		return ""
	}
	return "*." + rest
}
