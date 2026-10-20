package onedev

import (
	"github.com/taubyte/tau/pkg/forge"
)

// RouteFact is one row of the routing table, for the sweep: the requests this
// driver CAN issue, so a request no test reached fails rather than going
// uncovered.
type RouteFact struct {
	Method   string
	Template string
	Params   []string
	Declared string
	Ships    bool
	Matches  func(method, escapedPath string) bool
}

// DeclaredRoutes is every route this driver registered, matched against the
// base the given driver was built with.
func DeclaredRoutes(drv forge.Driver) []RouteFact {
	d, ok := drv.(*driver)
	if !ok {
		panic("forge/onedev: DeclaredRoutes was given something this package did not build")
	}
	routesMu.Lock()
	table := append([]*route(nil), routeTable...)
	routesMu.Unlock()

	out := make([]RouteFact, 0, len(table))
	for _, r := range table {
		out = append(out, RouteFact{
			Method: r.method, Template: r.template, Params: r.params,
			Declared: r.declared, Ships: r.ships,
			Matches: func(method, escapedPath string) bool { return r.matches(d.base, method, escapedPath) },
		})
	}
	return out
}
