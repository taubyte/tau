package gateway

import (
	"sort"
	"strings"

	"github.com/taubyte/tau/utils/regex"

	"github.com/taubyte/tau/pkg/specs"
)

// route.go decides which gateway resource serves a request, and where it goes.
//
// A GATEWAY RESOURCE IS ONE ROUTE. That is the vocabulary's shape, not a
// simplification here: `route:` is a block and not a list, so an author writing
// `/checkout` and `/api` on one name writes two gateways. Matching is therefore
// "which of the resources at this name wants this request", and the answer has
// to be the most specific one rather than whichever came back first.

// candidate is one gateway resource that answers on the requested name.
type candidate struct {
	host     specs.Host
	resource specs.Resource
}

// match is the winning route, decomposed.
type match struct {
	candidate

	// Path is what the route declared; Strip says the matched prefix comes off
	// before the request is handed on.
	Path  string
	Strip bool

	// Handler is what serves it.
	Handler specs.Resource
}

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
	type scored struct {
		match
		score int
	}

	var wins []scored
	for _, c := range candidates {
		route := c.resource.Sub("route")
		if len(route) == 0 {
			continue
		}

		// A GATEWAY ROUTED BY A FUNCTION MATCHES EVERY PATH, which is what
		// "there are no static routes" means — and it needs no branch here,
		// because the compiler emits `route: {match: prefix}` with no path for
		// one. An empty prefix matches anything at score 0, so it sits below
		// every declared route including a bare `/`: where a name carries both,
		// the one that named a path wins and the function is the fallback.
		declared := route.String("path")
		score, ok := pathScore(route.String("match"), declared, path)
		if !ok {
			continue
		}

		if !methodAllowed(route.Strings("methods"), method) {
			methodOnly = true
			continue
		}

		strip, _ := route.Bool("strip_prefix")
		wins = append(wins, scored{
			match: match{
				candidate: c,
				Path:      declared,
				Strip:     strip,
				Handler:   route.Sub("handler"),
			},
			score: score,
		})
	}

	if len(wins) == 0 {
		return match{}, false, methodOnly
	}

	// THE MOST SPECIFIC WINS, and ties are broken by name so that two routes an
	// author wrote to be equally specific resolve the same way on every node.
	// A gateway where the answer depended on map order would serve one node's
	// traffic differently from another's, intermittently.
	sort.SliceStable(wins, func(i, j int) bool {
		if wins[i].score != wins[j].score {
			return wins[i].score > wins[j].score
		}
		return wins[i].host.Name < wins[j].host.Name
	})
	return wins[0].match, true, false
}

// pathScore says whether a route matches and how specifically.
//
// An exact match beats every prefix, and a longer prefix beats a shorter one —
// so `/` can be the catch-all a site needs without swallowing `/api`, which is
// the single most common shape in every imported compose file.
func pathScore(kind, declared, path string) (int, bool) {
	switch kind {
	case "exact":
		if declared == path {
			return 1 << 20, true
		}
	case "regex":
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

// prefixMatches is a SEGMENT prefix and not a string one.
//
// `/api` must not match `/apiary`, which a plain strings.HasPrefix does — and
// the difference is a route quietly serving a path its author never named.
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
// asked for it.
//
// A handler mounted at /docs receives /docs/x unless this is set. For an
// application that is usually right — it routes on the full path. For a website
// it is usually wrong: the site was built for /, so every page 404s while the
// config looks correct.
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
	if declared := m.Handler.String("namespace"); declared != "" {
		scope = splitScope(declared)
	}
	return specs.Address{
		Project:     projectName,
		Environment: environment,
		Scope:       scope,
		Kind:        m.Handler.String("kind"),
		Name:        m.Handler.String("name"),
	}
}

// splitScope turns "/shop/eu" into the chain a lookup takes. An empty or "/"
// namespace is the project root, which is what the vocabulary says an omitted
// one means.
func splitScope(namespace string) []string {
	namespace = strings.Trim(namespace, "/")
	if namespace == "" {
		return nil
	}
	return strings.Split(namespace, "/")
}

// router is the function that decides where this request goes, and the scope to
// resolve it in — empty name when the route declares its handler outright.
//
// TWO PLACES, ONE QUESTION. A gateway whose `routing` is `router` has no static
// routes; a single route whose `target` is `router` picks that route's handler.
// The route's own is the more specific of the two and wins where both are
// written.
// guard is the authorizer this route answers to: its own if it named one, and
// the gateway's otherwise.
func (m match) guard() (name string, scope []string) {
	if n := m.resource.Sub("route").Sub("authorizer").String("name"); n != "" {
		return n, scopeOrNil(m.resource.Sub("route").Sub("authorizer").String("namespace"))
	}
	if n := m.resource.Sub("authorizer").String("name"); n != "" {
		return n, scopeOrNil(m.resource.Sub("authorizer").String("namespace"))
	}
	return "", nil
}

func (m match) router() (name string, scope []string) {
	// THE ROUTE'S OWN ROUTER LIVES UNDER `route/router`, not under the handler.
	// This used to read `route/handler/name`, which is Required only when the
	// target IS a handler — so the guard above was true in exactly the case the
	// value below is empty, the branch never fired, and a route-routed gateway
	// fell through to an address with no kind and no name.
	if m.resource.String("route", "target") == "router" {
		if n := m.resource.String("route", "router", "name"); n != "" {
			return n, scopeOrNil(m.resource.String("route", "router", "namespace"))
		}
	}
	if m.resource.String("routing") == "router" {
		return m.resource.String("router", "name"), scopeOrNil(m.resource.String("router", "namespace"))
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
