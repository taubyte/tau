package basic

import (
	"net"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// applyHostMatch scopes a route to a set of hosts (a service's <svc>.tau.<fqdn>
// plus any custom domains bound via domains.hosts). mux's route.Host takes only
// one host, so a set is matched with a MatcherFunc. Empty entries are ignored,
// and no concrete host leaves the route host-agnostic (matches any) — same as a
// service that configures no host.
func applyHostMatch(route *mux.Route, hosts []string) {
	set := make(map[string]struct{}, len(hosts))
	for _, h := range hosts {
		if h != "" {
			set[strings.ToLower(h)] = struct{}{}
		}
	}
	if len(set) == 0 {
		return
	}
	route.MatcherFunc(func(r *http.Request, _ *mux.RouteMatch) bool {
		_, ok := set[hostOnly(r.Host)]
		return ok
	})
}

// hostOnly lowercases a Host header value and strips any :port (mux's own host
// matcher does the same, so single-Host and Hosts routes compare alike).
func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}
