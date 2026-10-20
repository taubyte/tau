package gogs

import (
	"net/url"

	"github.com/taubyte/tau/pkg/forge"
)

// THE ROUTING TABLE, FOR THE SWEEP. The rest of this package's tests are
// gogs_test — they drive the driver the way a consumer does — and the one
// thing the sweep needs from inside is the set of requests this driver CAN
// issue, which is the whole point of the table: a sweep that read only what it
// happened to observe would arm a branch nobody drove with nothing.
//
// It is data and a matcher rather than the route type itself, so nothing about
// how a path is composed leaks into a test that is supposed to read only what
// went out.

// RouteFact is one row of the table.
type RouteFact struct {
	// Method and Template are what the route says about itself.
	Method   string
	Template string
	// Params are the query parameters this route may carry. A route that
	// declares one the sweep never saw is a parameter armed by nothing.
	Params []string
	// Declared is the file and line newRoute was called at, so a failure names
	// the line that introduced the request rather than a number.
	Declared string
	// Ships is false for a route a test built to drive the plumbing.
	Ships bool
	// Matches reports whether a request that went out is this route's.
	Matches func(method, escapedPath string) bool
}

// DeclaredRoutes is every route this driver registered, matched against the API
// base the given driver was built with.
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
