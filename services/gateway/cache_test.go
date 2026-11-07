package gateway

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
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
