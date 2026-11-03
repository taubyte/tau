package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// Matching is tested directly: the cases are combinations no single fixture has.

func gw(name string, route map[string]any) candidate {
	return candidate{
		host:    specs.Host{Kind: "gateway", Name: name},
		gateway: specs.ReadGateway(specs.Resource{"name": name, "route": route}),
	}
}

func handler(kind, name string) map[string]any {
	return map[string]any{"kind": kind, "name": name}
}

func TestTheMostSpecificRouteWins(t *testing.T) {
	candidates := []candidate{
		gw("site", map[string]any{"path": "/", "match": "prefix", "handler": handler("package", "shop")}),
		gw("api", map[string]any{"path": "/api", "match": "prefix", "handler": handler("application", "api")}),
		gw("deep", map[string]any{"path": "/api/v2", "match": "prefix", "handler": handler("function", "v2")}),
	}

	type testCase struct{ path, want string }
	for _, c := range []testCase{
		{"/", "shop"},
		{"/about", "shop"},
		{"/api", "api"},
		{"/api/orders", "api"},
		{"/api/v2/orders", "v2"},
	} {
		m, found, _ := pick(candidates, c.path, "GET")
		require.True(t, found, c.path)
		require.Equal(t, c.want, m.handler().HandlerName, c.path)
	}
}

func TestAnExactMatchBeatsEveryPrefix(t *testing.T) {
	candidates := []candidate{
		gw("prefix", map[string]any{"path": "/api/v2/orders", "match": "prefix", "handler": handler("function", "prefixed")}),
		gw("exact", map[string]any{"path": "/api/v2/orders", "match": "exact", "handler": handler("function", "exact")}),
	}

	m, found, _ := pick(candidates, "/api/v2/orders", "GET")
	require.True(t, found)
	require.Equal(t, "exact", m.handler().HandlerName)

	// And an exact route claims nothing below it.
	m, found, _ = pick(candidates, "/api/v2/orders/1", "GET")
	require.True(t, found)
	require.Equal(t, "prefixed", m.handler().HandlerName)
}

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
	require.Equal(t, "user", m.handler().HandlerName)

	// A pattern that does not compile matches nothing rather than taking down
	// the other routes at this name.
	_, found, _ = pick(candidates, "/u/abc", "GET")
	require.False(t, found)
}

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

func TestTwoEquallySpecificRoutesAreStable(t *testing.T) {
	candidates := []candidate{
		gw("zebra", map[string]any{"path": "/x", "handler": handler("function", "z")}),
		gw("alpha", map[string]any{"path": "/x", "handler": handler("function", "a")}),
	}

	for range 20 {
		m, found, _ := pick(candidates, "/x", "GET")
		require.True(t, found)
		require.Equal(t, "a", m.handler().HandlerName)
	}
}

func TestAGatewayWithNoRouteBlockIsNotACandidate(t *testing.T) {
	_, found, _ := pick([]candidate{{
		host:    specs.Host{Kind: "gateway", Name: "routed"},
		gateway: specs.ReadGateway(specs.Resource{"name": "routed", "routing": "router"}),
	}}, "/anything", "GET")
	require.False(t, found)
}

func TestStripPrefix(t *testing.T) {
	stripping := []candidate{
		gw("docs", map[string]any{"path": "/docs", "match": "prefix", "strip_prefix": true,
			"handler": handler("package", "docs")}),
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
		gw("root", map[string]any{"path": "/", "strip_prefix": true, "handler": handler("package", "shop")}),
	}
	m, _, _ = pick(root, "/about", "GET")
	require.Equal(t, "/about", m.stripped("/about"), "there is nothing to strip off the root")
}

func TestTheHandlersScope(t *testing.T) {
	inNamespace := candidate{
		host: specs.Host{Kind: "gateway", Name: "g", Scope: []string{"shop"}},
		gateway: specs.ReadGateway(specs.Resource{"name": "g", "route": map[string]any{
			"path": "/", "handler": handler("function", "notify"),
		}}),
	}
	m, found, _ := pick([]candidate{inNamespace}, "/", "GET")
	require.True(t, found)
	require.Equal(t, []string{"shop"}, m.address("shop", "prod").Scope,
		"the gateway's own scope, because the handler named none")

	elsewhere := candidate{
		host: specs.Host{Kind: "gateway", Name: "g", Scope: []string{"shop"}},
		gateway: specs.ReadGateway(specs.Resource{"name": "g", "route": map[string]any{
			"path": "/", "handler": map[string]any{"kind": "function", "name": "invoice", "namespace": "/billing"},
		}}),
	}
	m, _, _ = pick([]candidate{elsewhere}, "/", "GET")
	require.Equal(t, []string{"billing"}, m.address("shop", "prod").Scope)

	require.Nil(t, splitScope(""))
	require.Nil(t, splitScope("/"))
	require.Equal(t, []string{"shop", "local"}, splitScope("/shop/local"))
	require.Equal(t, []string{"shop", "local"}, splitScope("shop/local/"))
}

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

func TestAGatewayLevelRouterIsUnchanged(t *testing.T) {
	m := match{gateway: specs.ReadGateway(specs.Resource{"routing": "router",
		"router": map[string]any{"name": "everything"}})}

	name, _ := m.router()
	require.Equal(t, "everything", name)
}

func TestARoutesOwnAuthorizerIsReadWhereTheCompilerPutsIt(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../../node/dream/projects/with/bus"), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)
	raw, err := json.Marshal(obj.Flat())
	require.NoError(t, err)
	p, err := specs.Load(raw)
	require.NoError(t, err)

	front, _, err := p.Resource(nil, specs.KindGateway, "front")
	require.NoError(t, err)
	name, scope := match{candidate: candidate{gateway: specs.ReadGateway(front)}}.guard()
	require.Equal(t, "doorman", name, "the route's own authorizer, as compiled")
	require.Nil(t, scope, "no namespace written, so the gateway's own scope")

	// A route's own answers instead of the gateway's.
	both := specs.ReadGateway(specs.Resource{
		"authorizer": map[string]any{"name": "everything"},
		"route":      map[string]any{"authorizer": map[string]any{"name": "this", "namespace": "/shop"}},
	})
	name, scope = match{candidate: candidate{gateway: both}}.guard()
	require.Equal(t, "this", name)
	require.Equal(t, []string{"shop"}, scope)

	name, _ = match{candidate: candidate{gateway: specs.ReadGateway(specs.Resource{
		"authorizer": map[string]any{"name": "everything"},
	})}}.guard()
	require.Equal(t, "everything", name, "and the gateway's covers a route that names none")
}
