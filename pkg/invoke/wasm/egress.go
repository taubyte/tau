package wasm

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/netguard"
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
// # So it is governed, and the vocabulary says exactly how
//
// `egress` is "the per-workload ALLOWLIST of hosts a workload may reach,
// layered on top of the node-wide deny floor (pkg/netguard) which it can never
// widen... absent means the floor alone."
//
// So there are two rules and both apply:
//
//	THE FLOOR ALWAYS. No node-local service, no cloud metadata endpoint, no
//	private or link-local range — enforced at the dial, on the resolved IP,
//	because a name is not what a packet is addressed to.
//	THE ALLOWLIST WHEN THERE IS ONE. A resource that declared hosts reaches
//	those and no others.
//
// AN ABSENT LIST IS THE FLOOR ALONE AND NOT "NOWHERE", which is the
// vocabulary's own sentence and the opposite of what this file said first. A
// resource that names nothing is a resource that asked for no NARROWING, not
// one that asked for isolation — and reading it the other way would have made
// every function that calls an API fail with a message about a list its author
// never knew to write.
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
		next = floor()
	}

	// AN ABSENT LIST IS THE FLOOR ALONE. The floor is in the transport, so
	// there is nothing left for this layer to say.
	if len(hosts) == 0 {
		return &http.Client{Transport: next}
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

// floor is the node-wide deny floor as a transport: every dial goes through
// netguard, which refuses a connection to a private range, a link-local
// address, or the cloud metadata endpoint.
//
// IT IS ENFORCED ON THE RESOLVED IP AND NOT ON THE NAME, because a name is not
// what a packet is addressed to — a host that resolves to 169.254.169.254 is
// the metadata endpoint whatever it is called, and that is the whole trick this
// exists to stop.
func floor() http.RoundTripper {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = netguard.RestrictedDialer(dialTimeout).DialContext
	return transport
}

// dialTimeout bounds reaching a host that does not answer. It is short because
// a workload's own timeout is the budget that matters and this only stops a
// dial from consuming all of it.
const dialTimeout = 10 * time.Second

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
