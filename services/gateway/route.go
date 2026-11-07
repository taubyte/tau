package gateway

import (
	"strings"

	"github.com/taubyte/tau/utils/regex"

	"github.com/taubyte/tau/pkg/specs"
)

// A gateway resource is one route (`route:` is a block, not a list), so matching
// chooses the most specific of the resources at a name.

// candidate is one gateway resource that answers on the requested name.
type candidate struct {
	host    specs.Host
	gateway specs.Gateway

	// elsewhere is a route whose handler runs only in another region.
	elsewhere bool
}

// match is the winning route, decomposed.
type match struct {
	candidate

	// Path is what the route declared; Strip says the matched prefix comes off
	// before the request is handed on.
	Path  string
	Strip bool
}

// handler is what serves the winning route: its kind, name and namespace, as
// the route declared them.
func (m match) handler() specs.GatewayRoute { return m.gateway.Route }

// pick chooses the route that serves this request.
//
// It answers three different things and they are not the same:
//
//	found      the request is served
//	methodOnly a route matched the path and not the method — 405, not 404,
//	           because saying "no such page" for a POST to a GET-only route
//	           sends a caller looking for a bug in their URL
//	neither    404
func pick(candidates []candidate, path, method string) (m match, found, methodOnly bool) {
	best := -1
	for _, c := range candidates {
		// Declared alone would miss a compiler-written route: `match` is defaulted, so
		// `route: {match: prefix}` declares nothing.
		route := c.gateway.Route
		if !route.Declared && route.Match == "" {
			continue
		}

		// A gateway routed by a function compiles to `route: {match: prefix}` with no path,
		// which matches anything at score 0, below every declared route including `/`. The
		// function is the fallback.
		declared := route.Path
		score, ok := pathScore(route.Match, declared, path)
		if !ok {
			continue
		}

		if !methodAllowed(route.Methods, method) {
			methodOnly = true
			continue
		}

		// The most specific wins, ties broken by name, so every node resolves the same
		// way; among exact equals the first declared stays.
		if best >= 0 && (score < best || score == best && c.host.Name >= m.host.Name) {
			continue
		}
		best = score
		m = match{
			candidate: c,
			Path:      declared,
			Strip:     route.StripPrefix != nil && *route.StripPrefix,
		}
	}

	if best < 0 {
		return match{}, false, methodOnly
	}
	return m, true, false
}

// pathScore says whether a route matches and how specifically. An exact match beats
// every prefix and a longer prefix beats a shorter one, so `/` is a catch-all that
// does not swallow `/api`.
func pathScore(kind, declared, path string) (int, bool) {
	switch kind {
	case specs.GatewayRouteMatchExact:
		if declared == path {
			return 1 << 20, true
		}
	case specs.GatewayRouteMatchRegex:
		re, err := regex.Compile(declared)
		if err != nil {
			// A route whose pattern does not compile matches nothing. It is
			// not an error here: refusing the whole request would take down
			// the other routes at this name for a mistake in one of them.
			return 0, false
		}
		if re.MatchString(path) {
			// Between an exact match and a prefix: a pattern is deliberate,
			// but it says nothing about how much of the path it claims.
			return 1 << 19, true
		}
	default: // prefix, and the vocabulary's default
		if prefixMatches(declared, path) {
			return len(declared), true
		}
	}
	return 0, false
}

// prefixMatches is a segment prefix: `/api` must not match `/apiary`.
func prefixMatches(declared, path string) bool {
	if declared == "" || declared == "/" {
		return true
	}
	declared = strings.TrimSuffix(declared, "/")
	if path == declared {
		return true
	}
	return strings.HasPrefix(path, declared+"/")
}

// methodAllowed reads the route's method list. An empty list is every method,
// which is what the vocabulary says omitting it means.
func methodAllowed(methods []string, method string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, m := range methods {
		if strings.EqualFold(m, method) {
			return true
		}
	}
	return false
}

// stripped is the path handed on, with the matched prefix removed if the route
// asked for it. A site built for / mounted at /docs needs it, or every page 404s.
func (m match) stripped(path string) string {
	if !m.Strip || m.Path == "" || m.Path == "/" {
		return path
	}
	out := strings.TrimPrefix(path, strings.TrimSuffix(m.Path, "/"))
	if out == "" {
		return "/"
	}
	return out
}

// address is where the request goes: the handler's kind and name, in the scope
// the route named — the handler's own `namespace`, or the gateway's if it said
// nothing.
func (m match) address(projectName, environment string) specs.Address {
	scope := m.host.Scope
	handler := m.handler()
	if declared := handler.HandlerNamespace; declared != "" {
		scope = splitScope(declared)
	}
	return specs.Address{
		Project:     projectName,
		Environment: environment,
		Scope:       scope,
		Kind:        handler.HandlerKind,
		Name:        handler.HandlerName,
	}
}

// splitScope turns "/shop/local" into the chain a lookup takes. An empty or "/"
// namespace is the project root, which is what the vocabulary says an omitted
// one means.
func splitScope(namespace string) []string {
	namespace = strings.Trim(namespace, "/")
	if namespace == "" {
		return nil
	}
	return strings.Split(namespace, "/")
}

// guard is the authorizer this route answers to: its own if it named one, and
// the gateway's otherwise.
func (m match) guard() (name string, scope []string) {
	if route := m.gateway.Route; route.AuthorizerName != "" {
		return route.AuthorizerName, scopeOrNil(route.AuthorizerNamespace)
	}
	if own := m.gateway.Authorizer; own.Name != "" {
		return own.Name, scopeOrNil(own.Namespace)
	}
	return "", nil
}

// router is the function that decides where this request goes, and the scope to
// resolve it in; an empty name when the route declares its handler outright. A
// gateway whose `routing` is `router` has no static routes; a route whose `target`
// is `router` picks that route's handler. The route's own wins where both exist.
func (m match) router() (name string, scope []string) {
	// The route's own router lives under `route/router`, not under the handler.
	if route := m.gateway.Route; route.Target == specs.GatewayRouteTargetRouter {
		if route.RouterName != "" {
			return route.RouterName, scopeOrNil(route.RouterNamespace)
		}
	}
	if m.gateway.Routing == specs.GatewayRoutingRouter {
		return m.gateway.Router.Name, scopeOrNil(m.gateway.Router.Namespace)
	}
	return "", nil
}

// scopeOrNil is a declared namespace as a chain, or nil to mean "the asking
// resource's own" — which is not the same as the project root.
func scopeOrNil(namespace string) []string {
	if namespace == "" {
		return nil
	}
	return splitScope(namespace)
}
