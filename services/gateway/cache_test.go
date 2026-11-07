package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// caching is the basic fixture with its mcp route answering any method and keeping
// answers for a minute, varying on the named headers.
func caching(t *testing.T, vary ...string) []byte {
	t.Helper()
	var flat map[string]any
	require.NoError(t, json.Unmarshal(compiled(t), &flat))

	var found bool
	for _, v := range flat["object"].(map[string]any)[gatewayKind].(map[string]any) {
		gw := v.(map[string]any)
		if gw["name"] != "tools" {
			continue
		}
		found = true
		route := gw["route"].(map[string]any)
		delete(route, "methods")
		route["cache"] = map[string]any{"ttl": int64(time.Minute), "vary": vary}
	}
	require.True(t, found)

	b, err := json.Marshal(flat)
	require.NoError(t, err)
	return b
}

func (w *recordingWorkers) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.calls)
}

func TestARouteThatCachesAnswersOnceThenFromWhatItKept(t *testing.T) {
	h := newHarnessOf(t, caching(t), Config{})

	first := h.do(t, http.MethodGet, "http://mcp.example.com/tools", "")
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, "miss", first.Header().Get(cacheHeader))
	require.Equal(t, 1, h.Workers.count())

	h.clock.advance(10 * time.Second)
	again := h.do(t, http.MethodGet, "http://mcp.example.com/tools", "")
	require.Equal(t, http.StatusOK, again.Code)
	require.Equal(t, "hit", again.Header().Get(cacheHeader))
	require.Equal(t, "10", again.Header().Get("Age"))
	require.Equal(t, first.Body.String(), again.Body.String())
	require.Equal(t, "mcp/tools", again.Header().Get("X-Served"))
	require.Equal(t, 1, h.Workers.count(), "the handler was not asked again")

	// ANOTHER PATH, ANOTHER QUERY OR ANOTHER METHOD is another answer.
	h.do(t, http.MethodGet, "http://mcp.example.com/other", "")
	h.do(t, http.MethodGet, "http://mcp.example.com/tools?page=2", "")
	h.do(t, http.MethodHead, "http://mcp.example.com/tools", "")
	require.Equal(t, 4, h.Workers.count())

	// A WRITE IS NEVER KEPT OR ANSWERED FROM WHAT IS.
	post := h.do(t, http.MethodPost, "http://mcp.example.com/tools", "x")
	require.Empty(t, post.Header().Get(cacheHeader))
	require.Equal(t, 5, h.Workers.count())

	// PAST ITS TTL it is asked again.
	h.clock.advance(time.Minute)
	stale := h.do(t, http.MethodGet, "http://mcp.example.com/tools", "")
	require.Equal(t, "miss", stale.Header().Get(cacheHeader))
	require.Equal(t, 6, h.Workers.count())
}

func TestARouteThatSaysNothingKeepsNothing(t *testing.T) {
	h := newHarness(t, Config{})
	for range 2 {
		w := h.do(t, http.MethodPost, "http://mcp.example.com/tools", "")
		require.Empty(t, w.Header().Get(cacheHeader))
	}
	require.Equal(t, 2, h.Workers.count())
}

func TestTheHandlersCacheControlWins(t *testing.T) {
	type testCase struct {
		name    string
		headers map[string][]string
		status  int
		wait    time.Duration
		kept    bool
	}
	for _, tc := range []testCase{
		{name: "no-store", headers: map[string][]string{"Cache-Control": {"no-store"}}},
		{name: "no-cache", headers: map[string][]string{"Cache-Control": {"public, no-cache"}}},
		{name: "private", headers: map[string][]string{"Cache-Control": {"private, max-age=600"}}},
		{name: "a cookie", headers: map[string][]string{"Set-Cookie": {"session=1"}}},
		{name: "max-age=0", headers: map[string][]string{"Cache-Control": {"max-age=0"}}},
		{name: "an error", status: http.StatusInternalServerError},
		{name: "max-age outlives the ttl", headers: map[string][]string{"Cache-Control": {"max-age=600"}}, wait: 5 * time.Minute, kept: true},
		{name: "s-maxage beats max-age", headers: map[string][]string{"Cache-Control": {"max-age=600, s-maxage=5"}}, wait: 10 * time.Second},
		{name: "a malformed age keeps nothing", headers: map[string][]string{"Cache-Control": {"max-age=soon"}}},
		{name: "a not found is kept", status: http.StatusNotFound, wait: 30 * time.Second, kept: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessOf(t, caching(t), Config{})
			status := tc.status
			if status == 0 {
				status = http.StatusOK
			}
			h.Workers.reply = &invoke.Response{Status: status, Headers: tc.headers, Body: []byte("answer")}

			h.do(t, http.MethodGet, "http://mcp.example.com/tools", "")
			h.clock.advance(tc.wait)
			second := h.do(t, http.MethodGet, "http://mcp.example.com/tools", "")
			if tc.kept {
				require.Equal(t, "hit", second.Header().Get(cacheHeader))
				require.Equal(t, 1, h.Workers.count())
				return
			}
			require.Equal(t, "miss", second.Header().Get(cacheHeader))
			require.Equal(t, 2, h.Workers.count())
		})
	}
}

func TestAnAnswerVariesOnTheHeadersTheRouteNames(t *testing.T) {
	h := newHarnessOf(t, caching(t, "Accept-Language"), Config{})

	h.do(t, http.MethodGet, "http://mcp.example.com/tools", "", "Accept-Language", "fr")
	fr := h.do(t, http.MethodGet, "http://mcp.example.com/tools", "", "Accept-Language", "fr", "X-Other", "1")
	require.Equal(t, "hit", fr.Header().Get(cacheHeader), "a header the route does not name changes nothing")
	en := h.do(t, http.MethodGet, "http://mcp.example.com/tools", "", "Accept-Language", "en")
	require.Equal(t, "miss", en.Header().Get(cacheHeader))
	require.Equal(t, 2, h.Workers.count())
}

func TestADeployIsNeverAnsweredFromTheOneBefore(t *testing.T) {
	bytes := caching(t)
	h := newHarnessOf(t, bytes, Config{})
	h.do(t, http.MethodGet, "http://mcp.example.com/tools", "")
	require.Equal(t, "hit", h.do(t, http.MethodGet, "http://mcp.example.com/tools", "").Header().Get(cacheHeader))

	// The same project, a different object: what a deploy publishes.
	var flat map[string]any
	require.NoError(t, json.Unmarshal(bytes, &flat))
	flat["object"].(map[string]any)["description"] = "the next release"
	next, err := json.Marshal(flat)
	require.NoError(t, err)
	p, err := specs.Load(next)
	require.NoError(t, err)
	_, err = h.Reg.Publish(t.Context(), register.Publication{Project: "shop", Environment: "prod", Revision: 2, Compiled: next, Hosts: hostsOf(t, p, "")})
	require.NoError(t, err)

	require.Equal(t, "miss", h.do(t, http.MethodGet, "http://mcp.example.com/tools", "").Header().Get(cacheHeader))
	require.Equal(t, 2, h.Workers.count())
}

func TestWhatIsKeptIsBounded(t *testing.T) {
	a := newAnswers(800)
	now := time.Unix(0, 0)
	put := func(path string, body int) answerKey {
		k := &kept{key: answerKey{path: path}, body: make([]byte, body), stored: now, expires: now.Add(time.Hour)}
		a.put(k)
		return k.key
	}

	// More than an eighth of the budget is not kept at all.
	tooBig := put("/big", 200)
	_, ok := a.get(tooBig, now)
	require.False(t, ok)

	first := put("/a", 90)
	second := put("/b", 90)
	_, ok = a.get(first, now)
	require.True(t, ok, "touched, so /b is now the oldest")
	for i := range 7 {
		put(string(rune('c'+i)), 90)
	}
	_, ok = a.get(second, now)
	require.False(t, ok, "the least recently used went first")
	_, ok = a.get(first, now)
	require.True(t, ok)
	require.LessOrEqual(t, a.held, a.budget)

	// Kept again under the same key, it replaces rather than adds.
	held := a.held
	put("/a", 90)
	require.Equal(t, held, a.held)
}

func TestACacheBudgetOfNothingIsRefused(t *testing.T) {
	_, err := Config{CacheBudget: -1}.withDefaults()
	require.ErrorContains(t, err, "a cache budget of nothing keeps nothing")
}

// edgeIn is a route answered in both regions, in front of a function in /eu that
// runs in region-1 alone, as region's view.
func edgeIn(t *testing.T, region string) []byte {
	t.Helper()
	fs := afero.NewMemMapFs()
	for path, body := range map[string]string{
		"/project.yaml": "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: shop\n" +
			"regions:\n    topology: active\n    in: [region-1, region-2]\nenvironments:\n    prod:\n        branches: [\"*\"]\n",
		"/namespace/eu/meta.yaml":         "id: QmTXGKe63GYuKA54yerPQmGyEtFjhUF5p1g2hahTpm6pkA\nregions:\n    in: [region-1]\n",
		"/namespace/eu/function/api.yaml": "id: QmPjgiv816ygGgxYKEFHhDoabVar1Cv6CAQvEa8aFubUtG\npackage:\n    name: api_code\n    namespace: /\nexecution:\n    call: handle\n    timeout: 15s\n    memory: 32MB\n",
		"/package/api_code.yaml":          "id: Qmf59hm7Wwxc4ZCNbqyF9N6zqKS1fVXB6tugiwHgEHpk2J\ntype: wasm\nsource: services/api\noutput: x.wasm\nbuild:\n    steps:\n        - name: build\n          image: rust:1.83\n          commands: [cargo build]\n",
		"/zone/example_com.yaml":          "id: QmXZy3vTBqdo6va5ouo2muAh8kHF4j8t8HWtpgyKoBVn7y\nfqdn: example.com\n",
		"/gateway/www.yaml": "id: QmWhQewj3uuPM9f3qodPJTPDUXE9BvG33zkyewVurWGHiU\nhost:\n    zone:\n        in:\n            name: example_com\n        name: www\n" +
			"route:\n    path: /\n    cache:\n        ttl: 1m\n    handler:\n        function:\n            namespace: /eu\n            name: api\n",
	} {
		require.NoError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	views, findings, err := schema.Views(obj.Flat(), schema.WithRegions(schema.WithRegion(region)))
	require.NoError(t, err, "%v", findings)
	b, err := json.Marshal(views[region].Object)
	require.NoError(t, err)
	return b
}

// A ROUTE'S HANDLER IN ANOTHER REGION is a route the compile allows. A node that
// reaches no other region refuses it by name, rather than answering as if the name
// served nothing.
func TestAHandlerInAnotherRegionIsRefusedByName(t *testing.T) {
	for region, want := range map[string]int{"region-1": http.StatusOK, "region-2": http.StatusBadGateway} {
		var said []string
		h := newHarnessOf(t, edgeIn(t, region), Config{}, WithLogger(func(format string, args ...any) {
			said = append(said, format)
		}))
		w := h.do(t, http.MethodGet, "http://www.example.com/", "")
		require.Equal(t, want, w.Code, region)
		if want == http.StatusBadGateway {
			require.Contains(t, w.Body.String(), "this route's handler runs in another region")
			require.Zero(t, h.Workers.count(), "nothing was asked to run it here")
			require.Len(t, said, 1)
		}
	}
}

// carrying is a Regions that answers from region-1, or fails with err.
type carrying struct {
	err   error
	calls []carriedCall
}

type carriedCall struct {
	route, handler specs.Address
	req            invoke.Request
}

func (c *carrying) Origin(_ context.Context, route, handler specs.Address, req *invoke.Request) (*invoke.Response, string, error) {
	c.calls = append(c.calls, carriedCall{route: route, handler: handler, req: *req})
	if c.err != nil {
		return nil, "", c.err
	}
	return &invoke.Response{Status: http.StatusOK, Body: []byte("from far away")}, "region-1", nil
}

// A ROUTE TO A HANDLER ANOTHER REGION RUNS IS CARRIED THERE, after this region ran
// what is the route's own, and what came back is kept like any answer.
func TestARouteIsCarriedToTheRegionRunningItsHandler(t *testing.T) {
	regions := &carrying{}
	h := newHarnessOf(t, edgeIn(t, "region-2"), Config{}, WithRegions(regions))

	w := h.do(t, http.MethodGet, "http://www.example.com/a?b=c", "", "X-Probe", "one")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "from far away", w.Body.String())
	require.Equal(t, "region-1", w.Header().Get(regionHeader))
	require.Equal(t, "miss", w.Header().Get("X-Taubyte-Cache"))
	require.Zero(t, h.Workers.count(), "nothing runs it here")

	require.Len(t, regions.calls, 1)
	call := regions.calls[0]
	require.Equal(t, specs.Address{Project: "shop", Environment: "prod", Kind: gatewayKind, Name: "www"}, call.route)
	require.Equal(t, specs.Address{Project: "shop", Environment: "prod", Scope: []string{"eu"}, Kind: specs.KindFunction, Name: "api"}, call.handler)
	require.Equal(t, http.MethodGet, call.req.Method)
	require.Equal(t, "/a", call.req.Path)
	require.Equal(t, "one", http.Header(call.req.Headers).Get("X-Probe"))

	kept := h.do(t, http.MethodGet, "http://www.example.com/a?b=c", "")
	require.Equal(t, "hit", kept.Header().Get("X-Taubyte-Cache"))
	require.Equal(t, "region-1", kept.Header().Get(regionHeader), "a kept answer still says where it came from")
	require.Len(t, regions.calls, 1, "answered from what was kept")
}

func TestARouteNoRegionAnswersIsRefusedByName(t *testing.T) {
	var said []string
	regions := &carrying{err: errors.New("the link is down")}
	h := newHarnessOf(t, edgeIn(t, "region-2"), Config{}, WithRegions(regions), WithLogger(func(format string, args ...any) {
		said = append(said, fmt.Sprintf(format, args...))
	}))

	w := h.do(t, http.MethodGet, "http://www.example.com/", "")
	require.Equal(t, http.StatusBadGateway, w.Code)
	require.Contains(t, w.Body.String(), "this route's handler runs in another region, which did not answer")
	require.NotContains(t, w.Body.String(), "the link is down", "the reason is the operator's")
	require.Len(t, said, 1)
	require.Contains(t, said[0], "the link is down")
}

// WHAT CROSSES IS ONE REQUEST FOR ONE ANSWER: a package's files, a port and a bus
// are refused by name rather than carried.
func TestOnlyARequestForAnAnswerIsCarried(t *testing.T) {
	regions := &carrying{}
	s := &Service{regions: regions, logf: func(string, ...any) {}}
	for _, route := range []specs.GatewayRoute{
		{HandlerKind: packageKind, HandlerName: "site"},
		{HandlerKind: busKind, HandlerName: "events"},
		{HandlerKind: specs.KindApplication, HandlerName: "shop", HandlerPort: "admin"},
	} {
		m := match{candidate: candidate{gateway: specs.Gateway{Route: route}, elsewhere: true}}
		code, why := s.unbuilt(m)
		require.Equal(t, http.StatusBadGateway, code)
		require.Equal(t, "this route's handler runs in another region, and is not carried between regions", why)
	}
	code, _ := s.unbuilt(match{candidate: candidate{gateway: specs.Gateway{Route: specs.GatewayRoute{HandlerKind: specs.KindFunction, HandlerName: "api"}}, elsewhere: true}})
	require.Zero(t, code)
	require.Empty(t, regions.calls)
}

func TestNilRegionsAreRefused(t *testing.T) {
	_, err := New(Config{}, WithRegions(nil))
	require.ErrorContains(t, err, "nil regions")
}
