package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

func TestTheWholeChain(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, http.MethodPost, "http://ask.example.com/", "a question")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "agent/support", w.Header().Get("X-Served"))

	seen := h.Workers.seen()
	require.Len(t, seen, 1)
	require.Equal(t, specs.Address{
		Project: "shop", Environment: "prod", Kind: "agent", Name: "support",
	}, seen[0].at)
	require.Equal(t, "POST", seen[0].req.Method)
	require.Equal(t, "/", seen[0].req.Path)
	require.Equal(t, []byte("a question"), seen[0].req.Body)
}

func TestANameNobodyOwnsIsNotFound(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, http.MethodGet, "http://nobody.example.com/", "")
	require.Equal(t, http.StatusNotFound, w.Code)
	require.NotContains(t, w.Body.String(), "shop")
	require.Empty(t, h.Workers.seen())
}

func TestAHostIsMatchedHoweverItWasTyped(t *testing.T) {
	h := newHarness(t, Config{})

	for _, host := range []string{"ask.example.com", "ASK.Example.com", "ask.example.com:8443", "ask.example.com."} {
		r := httptestRequest(http.MethodPost, "/", host)
		w := recorder()
		h.Gateway.Handler().ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code, host)
	}
}

func TestTheMethodIsPartOfTheRoute(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, http.MethodGet, "http://ask.example.com/", "")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)

	// `shop` serves only /checkout, so anything else at that name is a miss
	// rather than a method problem.
	w = h.do(t, http.MethodPost, "http://shop.example.com/nowhere", "")
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestAGeneratedNameLosesTheCloudsSegment(t *testing.T) {
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"})

	p, err := specs.Load(compiled(t))
	require.NoError(t, err)

	var generated string
	for _, host := range p.Hosts() {
		if host.Generated && host.Kind == "gateway" {
			generated = host.Fqdn
		}
	}
	require.NotEmpty(t, generated)

	w := h.do(t, http.MethodGet, "http://"+generated+".tau.example.io/", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "<h1>storefront</h1>", w.Body.String(), "the website's files, at its generated name")
}

func TestTheHandlerResolvesInTheRightScope(t *testing.T) {
	h := newHarness(t, Config{})

	h.do(t, http.MethodPost, "http://ask.example.com/", "")
	require.Empty(t, h.Workers.seen()[0].at.Scope, "the gateway is at the root and the handler named no namespace")
}

func TestCorsEchoesTheOriginThatMatched(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, http.MethodPost, "http://shop.example.com/checkout", "",
		"Origin", "https://shop.example.com")
	require.Equal(t, "https://shop.example.com", w.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, w.Header().Values("Vary"), "Origin")

	// cors is policy at the name, so it is set even on a failure; otherwise a 503 would
	// surface as a CORS error.
	require.Equal(t, http.StatusOK, w.Code)

	w = h.do(t, http.MethodPost, "http://shop.example.com/checkout", "",
		"Origin", "https://evil.example.com")
	require.Empty(t, w.Header().Get("Access-Control-Allow-Origin"),
		"cors is a browser's rule; the request is not refused here for it")
}

func TestAPreflightIsAnswered(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, http.MethodOptions, "http://shop.example.com/checkout", "",
		"Origin", "https://shop.example.com",
		"Access-Control-Request-Method", "POST",
		"Access-Control-Request-Headers", "content-type")
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, "https://shop.example.com", w.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "content-type", w.Header().Get("Access-Control-Allow-Headers"))
	require.Empty(t, h.Workers.seen(), "a preflight never reaches the workload")
}

func TestTheRateLimitIsTheAuthorsNumber(t *testing.T) {
	// `ask` is the fixture's tightest: 60 rpm, burst 10.
	h := newHarness(t, Config{})

	for i := range 10 {
		w := h.do(t, http.MethodPost, "http://ask.example.com/", "")
		require.Equal(t, http.StatusOK, w.Code, "request %d", i)
	}

	w := h.do(t, http.MethodPost, "http://ask.example.com/", "")
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.NotEmpty(t, w.Header().Get("Retry-After"))

	// And it refills: 60 per minute is one per second.
	h.clock.advance(2 * time.Second)
	w = h.do(t, http.MethodPost, "http://ask.example.com/", "")
	require.Equal(t, http.StatusOK, w.Code)
}

func TestAFailClosedTransformerThatCannotRunRefuses(t *testing.T) {
	h := newHarness(t, Config{})
	h.Workers.shapeErr = map[string]error{"redact": errors.New("the component would not start")}

	w := h.do(t, http.MethodPost, "http://shop.example.com/checkout", "a card number")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Empty(t, h.Workers.seen(), "and the body never reached the workload")
}

func TestATransformerShapesTheRequest(t *testing.T) {
	h := newHarness(t, Config{})
	h.Workers.shaped = map[string]*invoke.Outcome{
		"redact": {Forward: &invoke.Request{
			Method:  http.MethodPost,
			Path:    "/checkout",
			Headers: map[string][]string{"X-Redacted": {"yes"}},
			Body:    []byte("****"),
		}},
	}

	w := h.do(t, http.MethodPost, "http://shop.example.com/checkout", "a card number")
	require.Equal(t, http.StatusOK, w.Code)

	// The chain is a pipeline in declared order: the second transformer sees what the
	// first produced.
	asked := h.Workers.asked()
	require.Len(t, asked, 2, "both transformers ran")
	require.Equal(t, "redact", asked[0].at.Name, "in the order declared")
	require.Equal(t, "tier_header", asked[1].at.Name)
	require.Empty(t, asked[0].at.Scope, "redact names no namespace, so it resolves in the gateway's")
	require.Equal(t, []string{"shop"}, asked[1].at.Scope, "and tier_header names one")

	require.Equal(t, "a card number", string(asked[0].req.Body), "the first saw the original")
	require.Equal(t, "****", string(asked[1].req.Body), "and the second saw what the first produced")

	served := h.Workers.seen()
	require.Len(t, served, 1)
	require.Equal(t, "****", string(served[0].req.Body), "and the handler saw the end of the chain")
	require.Equal(t, []string{"yes"}, served[0].req.Headers["X-Redacted"])
}

func TestATransformerCanAnswerInsteadOfForwarding(t *testing.T) {
	h := newHarness(t, Config{})
	h.Workers.shaped = map[string]*invoke.Outcome{
		"redact": {Answer: &invoke.Response{
			Status:  http.StatusForbidden,
			Headers: map[string][]string{"X-Refused-By": {"redact"}},
			Body:    []byte("no card numbers here"),
		}},
	}

	w := h.do(t, http.MethodPost, "http://shop.example.com/checkout", "a card number")
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Equal(t, "redact", w.Header().Get("X-Refused-By"))
	require.Equal(t, "no card numbers here", w.Body.String())
	require.Empty(t, h.Workers.seen(), "the handler never ran")
}

func TestARouterRoutedNameAsksItsRouter(t *testing.T) {
	h := newHarness(t, Config{})
	h.Workers.routing = map[string]*invoke.Routing{
		"edge_router": {To: &invoke.Destination{Kind: "function", Name: "picked"}},
	}

	w := h.do(t, http.MethodGet, "http://api.example.com/anything", "")
	require.Equal(t, http.StatusOK, w.Code)

	asked := h.Workers.asksRouted()
	require.Len(t, asked, 1, "the router was asked")
	require.Equal(t, "edge_router", asked[0].at.Name)
	require.Equal(t, "/anything", asked[0].req.Path, "and it saw the request")

	served := h.Workers.seen()
	require.Len(t, served, 1)
	require.Equal(t, "picked", served[0].at.Name, "the handler is the one it named")
}

func TestARouterCanAnswerWithoutAHandler(t *testing.T) {
	h := newHarness(t, Config{})
	h.Workers.routing = map[string]*invoke.Routing{
		"edge_router": {Answer: &invoke.Response{
			Status:  http.StatusFound,
			Headers: map[string][]string{"Location": {"/elsewhere"}},
		}},
	}

	w := h.do(t, http.MethodGet, "http://api.example.com/anything", "")
	require.Equal(t, http.StatusFound, w.Code)
	require.Equal(t, "/elsewhere", w.Header().Get("Location"))
	require.Empty(t, h.Workers.seen(), "no handler ran")
}

func TestARouterThatMatchesNothingIs404AndOneThatFailsIsNot(t *testing.T) {
	h := newHarness(t, Config{})
	h.Workers.routing = map[string]*invoke.Routing{
		"edge_router": {None: "no rule matched"},
	}
	require.Equal(t, http.StatusNotFound,
		h.do(t, http.MethodGet, "http://api.example.com/anything", "").Code)

	h.Workers.routing = nil
	h.Workers.routeErr = map[string]error{"edge_router": errors.New("the component would not start")}
	require.Equal(t, http.StatusServiceUnavailable,
		h.do(t, http.MethodGet, "http://api.example.com/anything", "").Code,
		"a router that could not be run must not read as a missing path")
}

func TestABodyLargerThanTheBudgetIsRefused(t *testing.T) {
	h := newHarness(t, Config{MaxBody: 8})

	w := h.do(t, http.MethodPost, "http://ask.example.com/", "more than eight bytes")
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	require.Empty(t, h.Workers.seen())
}

func TestWhatTheWorkerSaysBecomesWhatTheClientIsTold(t *testing.T) {
	type testCase struct {
		err  error
		want int
	}
	for _, c := range []testCase{
		{specs.ErrNotFound, http.StatusNotFound},
		{register.ErrNotPublished, http.StatusNotFound},
		{invoke.ErrAtCapacity, http.StatusServiceUnavailable},
		{errors.New("a tier that is not registered"), http.StatusBadGateway},
	} {
		h := newHarness(t, Config{})
		h.Workers.fail(c.err)

		w := h.do(t, http.MethodPost, "http://ask.example.com/", "")
		require.Equal(t, c.want, w.Code, "%v", c.err)
		require.NotContains(t, w.Body.String(), "shop", "the client is told nothing about the deployment")
	}
}

func TestTheWorkersAnswerIsPassedThrough(t *testing.T) {
	h := newHarness(t, Config{})
	h.Workers.answer(&invoke.Response{
		Status:  http.StatusTeapot,
		Headers: map[string][]string{"Set-Cookie": {"a=1", "b=2"}},
		Body:    []byte("brewed"),
	})

	w := h.do(t, http.MethodPost, "http://ask.example.com/", "")
	require.Equal(t, http.StatusTeapot, w.Code)
	require.Equal(t, []string{"a=1", "b=2"}, w.Header().Values("Set-Cookie"))
	require.Equal(t, "brewed", w.Body.String())

	// A workload that said nothing about status gets 200 rather than 0.
	h.Workers.answer(&invoke.Response{Body: []byte("ok")})
	w = h.do(t, http.MethodPost, "http://ask.example.com/", "")
	require.Equal(t, http.StatusOK, w.Code)
}

func TestTheHostTableIsRemembered(t *testing.T) {
	h := newHarness(t, Config{HostTTL: 30 * time.Second})

	counting := &countingHosts{inner: h.Reg}
	s, err := New(Config{HostTTL: 30 * time.Second},
		WithHosts(counting), WithSource(h.Reg), WithWorkers(h.Workers), WithClock(h.clock.Now))
	require.NoError(t, err)

	for range 5 {
		r := httptestRequest(http.MethodPost, "/", "ask.example.com")
		w := recorder()
		s.Handler().ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)
	}
	require.Equal(t, 1, counting.count())

	h.clock.advance(31 * time.Second)

	r := httptestRequest(http.MethodPost, "/", "ask.example.com")
	s.Handler().ServeHTTP(recorder(), r)
	require.Equal(t, 2, counting.count())

	projects, names := s.Held()
	require.Equal(t, 1, projects)
	require.Equal(t, 1, names)
}

func TestTheHostTableIsBounded(t *testing.T) {
	h := newHarness(t, Config{MaxHosts: 2})

	for _, name := range []string{"shop.example.com", "ask.example.com", "api.example.com"} {
		r := httptestRequest(http.MethodPost, "/", name)
		h.Gateway.Handler().ServeHTTP(recorder(), r)
	}

	_, names := h.Gateway.Held()
	require.LessOrEqual(t, names, 2)
}

func TestAnUnreachableHostTableIsNotAMissingName(t *testing.T) {
	h := newHarness(t, Config{})

	s, err := New(Config{},
		WithHosts(brokenHosts{}), WithSource(h.Reg), WithWorkers(h.Workers), WithClock(h.clock.Now))
	require.NoError(t, err)

	r := httptestRequest(http.MethodPost, "/", "ask.example.com")
	w := recorder()
	s.Handler().ServeHTTP(w, r)
	require.Equal(t, http.StatusBadGateway, w.Code)
}

func TestARequestWithNoHostIsRefused(t *testing.T) {
	h := newHarness(t, Config{})

	r := httptestRequest(http.MethodGet, "/", "")
	r.Host = ""
	r.URL.Host = ""
	w := recorder()
	h.Gateway.Handler().ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStatusFor(t *testing.T) {
	type testCase struct {
		err  error
		want int
	}
	for _, c := range []testCase{
		{specs.ErrNotFound, http.StatusNotFound},
		{register.ErrNotPublished, http.StatusNotFound},
		{invoke.ErrAtCapacity, http.StatusServiceUnavailable},
		{context.DeadlineExceeded, http.StatusGatewayTimeout},
		{connect.NewError(connect.CodeNotFound, errors.New("gone")), http.StatusNotFound},
		{connect.NewError(connect.CodeResourceExhausted, errors.New("full")), http.StatusServiceUnavailable},
		{connect.NewError(connect.CodeDeadlineExceeded, errors.New("slow")), http.StatusGatewayTimeout},
		{connect.NewError(connect.CodeFailedPrecondition, errors.New("no tier")), http.StatusBadGateway},
		{errors.New("something else"), http.StatusBadGateway},
	} {
		require.Equal(t, c.want, statusFor(c.err), "%v", c.err)
	}
}

func TestAnEnvironmentThatCannotBeRead(t *testing.T) {
	h := newHarness(t, Config{})

	// The register claims the name and holds no object for it, as on a node with a
	// half-replicated publish.
	s, err := New(Config{},
		WithHosts(h.Reg), WithSource(emptySource{}), WithWorkers(h.Workers), WithClock(h.clock.Now))
	require.NoError(t, err)

	r := httptestRequest(http.MethodPost, "/", "ask.example.com")
	w := recorder()
	s.Handler().ServeHTTP(w, r)
	require.Equal(t, http.StatusBadGateway, w.Code)
}

func TestANameOwnedWithNothingAtIt(t *testing.T) {
	h := newHarness(t, Config{})

	// Republish the same project claiming a name it does not serve.
	_, err := h.Reg.Publish(t.Context(), register.Publication{
		Project: "shop", Environment: "prod", Revision: 2,
		Compiled: compiled(t), Hosts: []string{"stale.example.com"},
	})
	require.NoError(t, err)

	h.clock.advance(time.Minute)

	w := h.do(t, http.MethodGet, "http://stale.example.com/", "")
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, h.Workers.seen())
}

func TestAProxysNameIsNotServedHere(t *testing.T) {
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"})

	p, err := specs.Load(compiled(t))
	require.NoError(t, err)

	var proxy string
	for _, host := range p.Hosts() {
		if host.Kind == "proxy" {
			proxy = host.Fqdn
		}
	}
	require.NotEmpty(t, proxy)

	w := h.do(t, http.MethodGet, "http://"+proxy+".tau.example.io/", "")
	require.Equal(t, http.StatusNotFound, w.Code)
}

// meter records what crossed the edge, including everything refused before a
// worker was asked.
type meter struct {
	mu     sync.Mutex
	events []Accounted
}

func (m *meter) Record(_ context.Context, ev Accounted) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, ev)
}

// recorded is every event so far. An attachment is emitted when the socket closes,
// so its test waits for it.
func (m *meter) recorded() []Accounted {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Accounted(nil), m.events...)
}

func (m *meter) only(t *testing.T) Accounted {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	require.Len(t, m.events, 1)
	return m.events[0]
}

func TestAServedRequestIsAccounted(t *testing.T) {
	m := &meter{}
	h := newHarness(t, Config{}, WithMeter(m))

	res := h.do(t, http.MethodPost, "http://ask.example.com/", "a body")
	require.Equal(t, http.StatusOK, res.Code)

	ev := m.only(t)
	require.Equal(t, OpServe, ev.Op)
	require.Equal(t, "ask.example.com", ev.Host)
	require.Equal(t, "shop", ev.Project)
	require.Equal(t, "prod", ev.Environment)
	require.Equal(t, http.StatusOK, ev.Status)
	require.Equal(t, int64(len("a body")), ev.In)
	require.Positive(t, ev.Out, "and what the workload answered with")
}

func TestAnUnknownNameIsAccountedAgainstNoProject(t *testing.T) {
	m := &meter{}
	h := newHarness(t, Config{}, WithMeter(m))

	res := h.do(t, http.MethodGet, "http://nobody.example.com/", "")
	require.Equal(t, http.StatusNotFound, res.Code)

	ev := m.only(t)
	require.Equal(t, OpRefuse, ev.Op)
	require.Equal(t, "nobody.example.com", ev.Host)
	require.Empty(t, ev.Project)
	require.Equal(t, http.StatusNotFound, ev.Status)
}

func TestARefusedRouteIsAccounted(t *testing.T) {
	m := &meter{}
	h := newHarness(t, Config{}, WithMeter(m))

	// The name has a POST-only route, so a GET is a 405 refusal a worker never hears
	// about.
	res := h.do(t, http.MethodGet, "http://ask.example.com/nothing-here", "")
	require.Equal(t, http.StatusMethodNotAllowed, res.Code)

	ev := m.only(t)
	require.Equal(t, OpRefuse, ev.Op)
	require.Equal(t, "shop", ev.Project, "the name resolved even though the route did not")
	require.Equal(t, http.StatusMethodNotAllowed, ev.Status)
}

func TestEveryExitIsAccounted(t *testing.T) {
	for name, url := range map[string]string{
		"unknown name": "http://nobody.example.com/",
		"no route":     "http://ask.example.com/nope",
		"served":       "http://ask.example.com/",
	} {
		m := &meter{}
		h := newHarness(t, Config{}, WithMeter(m))
		h.do(t, http.MethodGet, url, "")

		ev := m.only(t)
		require.NotZero(t, ev.Status, name)
		require.NotZero(t, ev.Op, name)
	}
}

func TestNoMeterIsTheOrdinaryCase(t *testing.T) {
	h := newHarness(t, Config{})
	require.Equal(t, http.StatusOK, h.do(t, http.MethodPost, "http://ask.example.com/", "").Code)
}

func TestWithMeterRefusesNil(t *testing.T) {
	require.Error(t, WithMeter(nil)(&options{}))
}

func TestAWorkloadThatCannotServeIsLoggedWithItsReason(t *testing.T) {
	var said []string
	h := newHarness(t, Config{}, WithLogger(func(format string, args ...any) {
		said = append(said, fmt.Sprintf(format, args...))
	}))
	h.Workers.fail(errors.New("the container answered nothing on 127.0.0.1:42759"))

	w := h.do(t, http.MethodPost, "http://ask.example.com/", "")

	require.Equal(t, http.StatusBadGateway, w.Code)
	require.NotContains(t, w.Body.String(), "127.0.0.1", "the reason is the operator's, not the caller's")
	require.Len(t, said, 1)
	require.Contains(t, said[0], "the container answered nothing on 127.0.0.1:42759")
	require.Contains(t, said[0], "shop/prod")

	_, err := New(Config{}, WithLogger(nil))
	require.Error(t, err)
}

// undeclared is a body whose length the request does not state, as a chunked
// upload arrives.
type undeclared struct{ io.Reader }

func (undeclared) Close() error { return nil }

func TestABodyOfUnknownLengthIsStillBoundedAndReadWhole(t *testing.T) {
	h := newHarness(t, Config{MaxBody: 8})
	send := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://ask.example.com/", nil)
		r.Body, r.ContentLength = undeclared{strings.NewReader(body)}, -1
		w := httptest.NewRecorder()
		h.Gateway.Handler().ServeHTTP(w, r)
		return w
	}

	require.Equal(t, http.StatusRequestEntityTooLarge, send("more than eight bytes").Code)
	require.Empty(t, h.Workers.seen())

	require.Equal(t, http.StatusOK, send("eight!!!").Code)
	require.Equal(t, []byte("eight!!!"), h.Workers.seen()[0].req.Body)
}

func TestARequestWithNoBodyHandsOnNone(t *testing.T) {
	h := newHarness(t, Config{})
	require.Equal(t, http.StatusOK, h.do(t, http.MethodPost, "http://ask.example.com/", "").Code)
	require.Empty(t, h.Workers.seen()[0].req.Body)

	require.Equal(t, http.StatusOK, h.do(t, http.MethodPost, "http://ask.example.com/", "exactly this").Code)
	require.Equal(t, []byte("exactly this"), h.Workers.seen()[1].req.Body, "a declared length is read whole")
}
