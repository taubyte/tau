package gogs

import (
	"net/url"

	"github.com/taubyte/tau/pkg/forge"
)

// The routing table, for the sweep: the requests this driver can issue.

type RouteFact struct {
	Method   string
	Template string
	// Params are the query parameters this route may carry. A route that
	// declares one the sweep never saw is a parameter armed by nothing.
	Params []string
	// Declared is the file and line newRoute was called at, so a failure names
	// the line that introduced the request rather than a number.
	Declared string
	// Ships is false for a route a test built to drive the plumbing.
	Ships   bool
	Matches func(method, escapedPath string) bool
}

func DeclaredRoutes(drv forge.Driver) []RouteFact {
	var base *url.URL
	switch d := drv.(type) {
	case *driver:
		base = d.base
	default:
		panic("forge/gogs: DeclaredRoutes was given something this package did not build")
	}

	routesMu.Lock()
	table := append([]*route(nil), routeTable...)
	routesMu.Unlock()

	out := make([]RouteFact, 0, len(table))
	for _, r := range table {
		out = append(out, RouteFact{
			Method:   r.method,
			Template: r.template,
			Params:   r.params,
			Declared: r.declared,
			Ships:    r.ships,
			Matches:  func(method, escapedPath string) bool { return r.matches(base, method, escapedPath) },
		})
	}
	return out
}
