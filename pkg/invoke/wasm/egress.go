package wasm

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/netguard"
)

// egress.go decides a guest's outbound HTTP (wasi:http/outgoing-handler, which most
// incoming-handler components import). Two rules: the node-wide floor always (no
// node-local, metadata, private or link-local destination, checked on the resolved
// IP at the dial), and the declared allowlist when there is one. An absent list is
// the floor alone, not "nowhere".

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

	// An absent list is the floor alone, already in the transport.
	if len(hosts) == 0 {
		return &http.Client{Transport: next}
	}

	allowed := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		// Lowercased and without a port: the author wrote a name, allowed on any port.
		allowed[strings.ToLower(strings.TrimSpace(host))] = true
	}

	return &http.Client{Transport: &allowlist{hosts: allowed, next: next}}
}

// floor is the node-wide deny floor as a transport: every dial goes through
// netguard, on the resolved IP, so a name resolving to 169.254.169.254 is still
// refused.
func floor() http.RoundTripper {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = netguard.RestrictedDialer(dialTimeout).DialContext
	return transport
}

// dialTimeout keeps an unanswering host from consuming the workload's whole
// timeout.
const dialTimeout = 10 * time.Second

func (a *allowlist) RoundTrip(req *http.Request) (*http.Response, error) {
	host := strings.ToLower(req.URL.Hostname())

	// A wildcard is one label: `*.stripe.com` allows api.stripe.com, not stripe.com or
	// a.b.stripe.com, as with certificates.
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
