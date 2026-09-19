package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

// route_test.go tests the matching directly, because the interesting cases are
// combinations no single fixture has: a catch-all beside a specific path, two
// routes of equal specificity, a pattern that does not compile.

func gw(name string, route map[string]any) candidate {
	return candidate{
		host:     specs.Host{Kind: "gateway", Name: name},
		resource: specs.Resource{"name": name, "route": route},
	}
}

func handler(kind, name string) map[string]any {
	return map[string]any{"kind": kind, "name": name}
}

// TestTheMostSpecificRouteWins. `/` is the catch-all a site needs and it must
// not swallow `/api`, which is the single most common shape in every imported
// compose file.
func TestTheMostSpecificRouteWins(t *testing.T) {
	candidates := []candidate{
		gw("site", map[string]any{"path": "/", "match": "prefix", "handler": handler("website", "shop")}),
		gw("api", map[string]any{"path": "/api", "match": "prefix", "handler": handler("application", "api")}),
		gw("deep", map[string]any{"path": "/api/v2", "match": "prefix", "handler": handler("function", "v2")}),
	}

	for _, c := range []struct{ path, want string }{
		{"/", "shop"},
		{"/about", "shop"},
		{"/api", "api"},
		{"/api/orders", "api"},
		{"/api/v2/orders", "v2"},
	} {
		m, found, _ := pick(candidates, c.path, "GET")
		require.True(t, found, c.path)
		require.Equal(t, c.want, m.Handler.String("name"), c.path)
	}
}

// TestAnExactMatchBeatsEveryPrefix, however long the prefix.
func TestAnExactMatchBeatsEveryPrefix(t *testing.T) {
	candidates := []candidate{
		gw("prefix", map[string]any{"path": "/api/v2/orders", "match": "prefix", "handler": handler("function", "prefixed")}),
		gw("exact", map[string]any{"path": "/api/v2/orders", "match": "exact", "handler": handler("function", "exact")}),
	}

	m, found, _ := pick(candidates, "/api/v2/orders", "GET")
	require.True(t, found)
	require.Equal(t, "exact", m.Handler.String("name"))

	// And an exact route claims nothing below it.
	m, found, _ = pick(candidates, "/api/v2/orders/1", "GET")
	require.True(t, found)
	require.Equal(t, "prefixed", m.Handler.String("name"))
}

// TestAPrefixIsSegmentwise. `/api` must not match `/apiary`, which a plain
// string prefix does — and the difference is a route quietly serving a path its
// author never named.
func TestAPrefixIsSegmentwise(t *testing.T) {
	candidates := []candidate{
		gw("api", map[string]any{"path": "/api", "match": "prefix", "handler": handler("function", "api")}),
	}

	_, found, _ := pick(candidates, "/apiary", "GET")
	require.False(t, found)

	_, found, _ = pick(candidates, "/api", "GET")
	require.True(t, found)

	_, found, _ = pick(candidates, "/api/", "GET")
	require.True(t, found)

	// A trailing slash on the declared path means the same route.
	trailing := []candidate{
		gw("api", map[string]any{"path": "/api/", "match": "prefix", "handler": handler("function", "api")}),
	}
	_, found, _ = pick(trailing, "/api/orders", "GET")
	require.True(t, found)
}

func TestARegexRouteMatchesAndABrokenOneMatchesNothing(t *testing.T) {
	candidates := []candidate{
		gw("re", map[string]any{"path": `^/u/[0-9]+$`, "match": "regex", "handler": handler("function", "user")}),
		gw("broken", map[string]any{"path": `^/u/[0-9+$`, "match": "regex", "handler": handler("function", "never")}),
	}

	m, found, _ := pick(candidates, "/u/42", "GET")
	require.True(t, found)
	require.Equal(t, "user", m.Handler.String("name"))

	// A pattern that does not compile matches nothing rather than taking down
	// the other routes at this name.
	_, found, _ = pick(candidates, "/u/abc", "GET")
	require.False(t, found)
}

// TestAMethodMissIsNotAPathMiss. Saying "no such page" for a POST to a GET-only
// route sends a caller looking for a bug in their URL.
func TestAMethodMissIsNotAPathMiss(t *testing.T) {
	candidates := []candidate{
		gw("api", map[string]any{"path": "/api", "match": "prefix", "methods": []any{"GET", "HEAD"},
			"handler": handler("function", "api")}),
	}

	_, found, methodOnly := pick(candidates, "/api", "POST")
	require.False(t, found)
	require.True(t, methodOnly)

	_, found, methodOnly = pick(candidates, "/nowhere", "POST")
	require.False(t, found)
	require.False(t, methodOnly)

	// Case does not matter, and an empty list is every method.
	_, found, _ = pick(candidates, "/api", "get")
	require.True(t, found)

	any := []candidate{gw("any", map[string]any{"path": "/", "handler": handler("function", "f")})}
	for _, method := range []string{"GET", "POST", "DELETE", "PROPFIND"} {
		_, found, _ = pick(any, "/x", method)
		require.True(t, found, method)
	}
}

// TestTwoEquallySpecificRoutesResolveTheSameWayEverywhere. A gateway where the
// answer depended on map order would serve one node's traffic differently from
// another's, intermittently.
func TestTwoEquallySpecificRoutesAreStable(t *testing.T) {
	candidates := []candidate{
		gw("zebra", map[string]any{"path": "/x", "handler": handler("function", "z")}),
		gw("alpha", map[string]any{"path": "/x", "handler": handler("function", "a")}),
	}

	for range 20 {
		m, found, _ := pick(candidates, "/x", "GET")
		require.True(t, found)
		require.Equal(t, "a", m.Handler.String("name"))
	}
}

// TestAGatewayWithNoRouteBlockIsNotACandidate — a router-routed one has none,
// and it must not be picked as if it had an empty path.
func TestAGatewayWithNoRouteBlockIsNotACandidate(t *testing.T) {
	_, found, _ := pick([]candidate{{
		host:     specs.Host{Kind: "gateway", Name: "routed"},
		resource: specs.Resource{"name": "routed", "routing": "router"},
	}}, "/anything", "GET")
	require.False(t, found)
}

// TestStripPrefix. A website built for / mounted at /docs 404s on every page
// while the config looks correct.
func TestStripPrefix(t *testing.T) {
	stripping := []candidate{
		gw("docs", map[string]any{"path": "/docs", "match": "prefix", "strip_prefix": true,
			"handler": handler("website", "docs")}),
	}
	m, found, _ := pick(stripping, "/docs/guide", "GET")
	require.True(t, found)
	require.Equal(t, "/guide", m.stripped("/docs/guide"))
	require.Equal(t, "/", m.stripped("/docs"), "the mount point itself becomes the root")

	keeping := []candidate{
		gw("docs", map[string]any{"path": "/docs", "match": "prefix", "handler": handler("application", "api")}),
	}
	m, _, _ = pick(keeping, "/docs/guide", "GET")
	require.Equal(t, "/docs/guide", m.stripped("/docs/guide"), "passing it through unchanged surprises nobody")

	root := []candidate{
		gw("root", map[string]any{"path": "/", "strip_prefix": true, "handler": handler("website", "shop")}),
	}
	m, _, _ = pick(root, "/about", "GET")
	require.Equal(t, "/about", m.stripped("/about"), "there is nothing to strip off the root")
}

// TestTheHandlersScopeIsItsOwnWhenItNamesOne, and the gateway's otherwise.
func TestTheHandlersScope(t *testing.T) {
	inNamespace := candidate{
		host: specs.Host{Kind: "gateway", Name: "g", Scope: []string{"shop"}},
		resource: specs.Resource{"name": "g", "route": map[string]any{
			"path": "/", "handler": handler("function", "notify"),
		}},
	}
	m, found, _ := pick([]candidate{inNamespace}, "/", "GET")
	require.True(t, found)
	require.Equal(t, []string{"shop"}, m.address("shop", "prod").Scope,
		"the gateway's own scope, because the handler named none")

	elsewhere := candidate{
		host: specs.Host{Kind: "gateway", Name: "g", Scope: []string{"shop"}},
		resource: specs.Resource{"name": "g", "route": map[string]any{
			"path": "/", "handler": map[string]any{"kind": "function", "name": "invoice", "namespace": "/billing"},
		}},
	}
	m, _, _ = pick([]candidate{elsewhere}, "/", "GET")
	require.Equal(t, []string{"billing"}, m.address("shop", "prod").Scope)

	require.Nil(t, splitScope(""))
	require.Nil(t, splitScope("/"))
	require.Equal(t, []string{"shop", "eu"}, splitScope("/shop/eu"))
	require.Equal(t, []string{"shop", "eu"}, splitScope("shop/eu/"))
}

// TestARouteNamesItsOwnRouter.
//
// A gateway whose `routing` is `router` has no static routes; a single route
// whose `target` is `router` picks that route's own. The second used to read
// `route/handler/name` — which the vocabulary requires only when the target IS
// a handler — so the guard was true in exactly the case the value was empty.
// The branch never fired, and the request fell through to an address with no
// kind and no name.
func TestARouteNamesItsOwnRouter(t *testing.T) {
	routed := []candidate{
		gw("edge", map[string]any{
			"path": "/", "match": "prefix", "target": "router",
			"router": map[string]any{"name": "pick", "namespace": "/shop"},
		}),
	}

	m, found, _ := pick(routed, "/anything", "GET")
	require.True(t, found)

	name, scope := m.router()
	require.Equal(t, "pick", name, "the route's own router was never read")
	require.Equal(t, []string{"shop"}, scope)
}

// TestAGatewayLevelRouterStillWins where no route declares one of its own.
func TestAGatewayLevelRouterIsUnchanged(t *testing.T) {
	m := match{resource: specs.Resource{"routing": "router",
		"router": map[string]any{"name": "everything"}}}

	name, _ := m.router()
	require.Equal(t, "everything", name)
}
