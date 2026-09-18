package gateway

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/register"
)

// TestTheWholeChain: a Host header arrives, the register says whose it is, the
// compiled project says what serves that name and path, and the request goes to
// the handler the author named.
//
// `ask` IS THE ROUTE THAT REACHES A WORKLOAD in this fixture. `shop` is the
// richer one and it declares a fail-closed transformer, so it deliberately does
// not serve — see TestAFailClosedTransformerRefuses, which is the more
// interesting test of the two.
func TestTheWholeChain(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, http.MethodPost, "http://ask.example.com/", "a question")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "agent/support", w.Header().Get("X-Served"))

	seen := h.Workers.seen()
	require.Len(t, seen, 1)
	require.Equal(t, project.Address{
		Project: "shop", Environment: "prod", Kind: "agent", Name: "support",
	}, seen[0].at)
	require.Equal(t, "POST", seen[0].req.Method)
	require.Equal(t, "/", seen[0].req.Path)
	require.Equal(t, []byte("a question"), seen[0].req.Body)
}

// TestANameNobodyOwnsIsNotFound, and the answer says nothing about anybody's
// deployment.
func TestANameNobodyOwnsIsNotFound(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, http.MethodGet, "http://nobody.example.com/", "")
	require.Equal(t, http.StatusNotFound, w.Code)
	require.NotContains(t, w.Body.String(), "shop")
	require.Empty(t, h.Workers.seen())
}

// TestAHostIsMatchedHoweverItWasTyped. DNS is case-insensitive and a Host
// header carries whatever the client typed.
func TestAHostIsMatchedHoweverItWasTyped(t *testing.T) {
	h := newHarness(t, Config{})

	for _, host := range []string{"ask.example.com", "ASK.Example.com", "ask.example.com:8443", "ask.example.com."} {
		r := httptestRequest(http.MethodPost, "/", host)
		w := recorder()
		h.Gateway.Handler().ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code, host)
	}
}

// TestTheMethodIsPartOfTheRoute. Saying "no such page" for a GET to a POST-only
// route sends a caller looking for a bug in their URL.
func TestTheMethodIsPartOfTheRoute(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, http.MethodGet, "http://ask.example.com/", "")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)

	// `shop` serves only /checkout, so anything else at that name is a miss
	// rather than a method problem.
	w = h.do(t, http.MethodPost, "http://shop.example.com/nowhere", "")
	require.Equal(t, http.StatusNotFound, w.Code)
}

// TestAGeneratedNameLosesTheCloudsSegment. The compiled project carries the
// part it can know; the node knows the rest and takes it off the request.
func TestAGeneratedNameLosesTheCloudsSegment(t *testing.T) {
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"})

	p, err := project.Load(compiled(t))
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
	require.Equal(t, "website/shop", w.Header().Get("X-Served"))
}

// TestTheHandlersScopeIsTheRoutesAndNotTheGatewaysWhenItSaysSo. A transformer
// or a handler can name a namespace; when it does not, the gateway's own scope
// is where the name resolves.
func TestTheHandlerResolvesInTheRightScope(t *testing.T) {
	h := newHarness(t, Config{})

	h.do(t, http.MethodPost, "http://ask.example.com/", "")
	require.Empty(t, h.Workers.seen()[0].at.Scope, "the gateway is at the root and the handler named no namespace")
}

// TestCorsEchoesTheOriginThatMatched, and never a wildcard: `*` cannot carry
// credentials and is a different policy from the list an author wrote.
func TestCorsEchoesTheOriginThatMatched(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, http.MethodPost, "http://shop.example.com/checkout", "",
		"Origin", "https://shop.example.com")
	require.Equal(t, "https://shop.example.com", w.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, w.Header().Values("Vary"), "Origin")

	// AND IT IS SET BEFORE THE ROUTE'S OWN GAPS. cors is policy at the NAME, so
	// a browser is told what is allowed even when the answer is a failure —
	// otherwise a fetch that got a 503 would be reported as a CORS error and
	// send somebody looking in the wrong place entirely. This route's
	// transformer runs now, so the answer here is the handler's.
	require.Equal(t, http.StatusOK, w.Code)

	w = h.do(t, http.MethodPost, "http://shop.example.com/checkout", "",
		"Origin", "https://evil.example.com")
	require.Empty(t, w.Header().Get("Access-Control-Allow-Origin"),
		"cors is a browser's rule; the request is not refused here for it")
}

// TestAPreflightIsAnsweredEvenWhereNoRouteTakesTheMethod. It asks what is
// allowed; it is not a request for a page.
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

// TestTheRateLimitIsTheAuthorsNumber, counted per node.
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

// TestAFailClosedTransformerThatCannotBeRunRefuses. `redact` strips card
// numbers out of request bodies on the way in and is declared fail-closed;
// serving its route without it would be the gateway deciding a security control
// was optional.
//
// THE FIRST VERSION OF THIS PASSED WITH A 200, and the bug was one accessor:
// transformers are a LIST in the compiled object, and reading them as a map
// returned nothing — so a fail-closed transformer silently stopped existing.
// It used to assert the refusal a gateway made because nothing could RUN a
// transformer; what it holds now is the narrower and more useful claim, that one
// which fails takes the route with it.
func TestAFailClosedTransformerThatCannotRunRefuses(t *testing.T) {
	h := newHarness(t, Config{})
	h.Workers.shapeErr = map[string]error{"redact": errors.New("the component would not start")}

	w := h.do(t, http.MethodPost, "http://shop.example.com/checkout", "a card number")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Empty(t, h.Workers.seen(), "and the body never reached the workload")
}

// TestATransformerRunsBeforeTheHandlerAndShapesWhatItSees.
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

	// THE CHAIN IS A PIPELINE AND THE ORDER IS THE DECLARATION'S. This route
	// names two, and what the second one sees is what the first produced —
	// otherwise the list would be independent opinions, none able to build on
	// another.
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

// TestATransformerCanANSWERAndTheHandlerNeverRuns, which is how a filter
// rejects: the request stops at the transformer and what it returned is what the
// client gets.
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

// TestARouterRoutedNameSaysSoRatherThanServingNothing.
func TestARouterRoutedNameIsNotImplemented(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, http.MethodGet, "http://api.example.com/anything", "")
	require.Equal(t, http.StatusNotImplemented, w.Code)
	require.Empty(t, h.Workers.seen())
}

// TestABodyLargerThanTheBudgetIsRefusedBeforeItIsBuffered.
func TestABodyLargerThanTheBudgetIsRefused(t *testing.T) {
	h := newHarness(t, Config{MaxBody: 8})

	w := h.do(t, http.MethodPost, "http://ask.example.com/", "more than eight bytes")
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	require.Empty(t, h.Workers.seen())
}

// TestWhatTheWorkerSaysBecomesWhatTheClientIsTold. The distinction is whose
// problem it is: a deployment missing a piece is a 502, a node at capacity is a
// 503 worth retrying.
func TestWhatTheWorkerSaysBecomesWhatTheClientIsTold(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int
	}{
		{project.ErrNotFound, http.StatusNotFound},
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

// TestTheWorkersAnswerIsPassedThrough, headers and all.
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

// TestTheHostTableIsRemembered, and separately from the pointer: a stale owner
// sends a request to the wrong PROJECT, where a stale pointer only serves the
// previous revision of the right one.
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

// TestTheHostTableIsBounded. A public listener sees every name anybody points
// at it, including ones nobody owns.
func TestTheHostTableIsBounded(t *testing.T) {
	h := newHarness(t, Config{MaxHosts: 2})

	for _, name := range []string{"shop.example.com", "ask.example.com", "api.example.com"} {
		r := httptestRequest(http.MethodPost, "/", name)
		h.Gateway.Handler().ServeHTTP(recorder(), r)
	}

	_, names := h.Gateway.Held()
	require.LessOrEqual(t, names, 2)
}

// TestAnUnreachableHostTableIsNotAMissingName. Answering 404 while the register
// is down would tell every client the site does not exist.
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

// TestEveryStatusTheClientCanBeTold, directly, because several of them come
// from failures a fixture cannot produce.
func TestStatusFor(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int
	}{
		{project.ErrNotFound, http.StatusNotFound},
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

// TestAnEnvironmentThatCannotBeReadIsNotAMissingSite. The name is claimed and
// the pointer exists, so the site is not missing — the platform cannot produce
// what it promised, which is the definition of a 502 and not a 404. A 404 here
// would send the owner looking at their DNS.
func TestAnEnvironmentThatCannotBeRead(t *testing.T) {
	h := newHarness(t, Config{})

	// The register claims the name and holds no object for it, which is what a
	// half-replicated publish looks like on a node that has the pointer and
	// not the bytes.
	s, err := New(Config{},
		WithHosts(h.Reg), WithSource(emptySource{}), WithWorkers(h.Workers), WithClock(h.clock.Now))
	require.NoError(t, err)

	r := httptestRequest(http.MethodPost, "/", "ask.example.com")
	w := recorder()
	s.Handler().ServeHTTP(w, r)
	require.Equal(t, http.StatusBadGateway, w.Code)
}

// TestANameThisNodeOwnsWithNothingAtIt. A deploy that removed the gateway but
// whose claim has not been released.
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

// TestAProxysNameIsNotAGatewaysName. A proxy answers on a name too, and it
// carries a raw connection: nothing here can parse it.
func TestAProxysNameIsNotServedHere(t *testing.T) {
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"})

	p, err := project.Load(compiled(t))
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

// ── what crossed the edge ────────────────────────────────────────────────

// BANDWIDTH IS THE THIRD BILLED UNIT and this is the only service that sees
// every byte in and out. A worker sees a request that already arrived; what
// crossed the edge — including everything refused before a worker was asked —
// is visible here and nowhere else.
type meter struct {
	mu     sync.Mutex
	events []Accounted
}

func (m *meter) Record(_ context.Context, ev Accounted) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, ev)
}

// recorded is every event so far. only() is the shape most tests want; an
// attachment is emitted when the socket closes, so its test waits for it.
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

// A REQUEST FOR A NAME NOTHING ANSWERS ON BELONGS TO NO PROJECT, which is a
// real answer and one a sink has to be able to total separately — the host is
// the only attribution it has.
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

// A FLOOD THAT NEVER REACHES A WORKLOAD costs this cloud bandwidth and CPU and
// shows up in no invocation count — which is exactly the traffic somebody
// disputing a bill asks about.
func TestARefusedRouteIsAccounted(t *testing.T) {
	m := &meter{}
	h := newHarness(t, Config{}, WithMeter(m))

	// The name has a route and it is POST-only, so a GET is refused with 405 —
	// which is a refusal like any other and is exactly the kind a worker never
	// hears about.
	res := h.do(t, http.MethodGet, "http://ask.example.com/nothing-here", "")
	require.Equal(t, http.StatusMethodNotAllowed, res.Code)

	ev := m.only(t)
	require.Equal(t, OpRefuse, ev.Op)
	require.Equal(t, "shop", ev.Project, "the name resolved even though the route did not")
	require.Equal(t, http.StatusMethodNotAllowed, ev.Status)
}

// EVERY EXIT IS ACCOUNTED, and there are many: a deferred emission is the only
// shape that survives one more being added.
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

// A node with no meter serves exactly as it did.
func TestNoMeterIsTheOrdinaryCase(t *testing.T) {
	h := newHarness(t, Config{})
	require.Equal(t, http.StatusOK, h.do(t, http.MethodPost, "http://ask.example.com/", "").Code)
}

func TestWithMeterRefusesNil(t *testing.T) {
	require.Error(t, WithMeter(nil)(&options{}))
}
