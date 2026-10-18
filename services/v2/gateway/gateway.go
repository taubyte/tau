// Package gateway is the way in. It is the only v2 service that faces the
// internet, and the only one that turns a request into a target.
//
// # What it does, per request
//
//	a Host header      -> whose environment this is        [the register]
//	that environment   -> what it currently runs           [register.Cache]
//	the name and path  -> which gateway resource serves it [the compiled config]
//	the route's handler-> a worker, over p2p
//
// EVERY POLICY COMES OFF THE CONFIG. CORS, rate limits, methods, path matching
// and prefix stripping are what the author wrote, per name, and none of them is
// an operator setting. What an operator declares is the cloud's own zone, the
// budgets that protect the node, and nothing about any project.
//
// # It chooses a route, never a node
//
// Which worker answers is a routing decision made from live measurements, and
// it is behind the Workers interface so that it stays in one place. A gateway
// that picked a peer itself would be a second router, disagreeing with the
// first under exactly the load that makes routing matter.
//
// # What it is not, yet
//
// IT DOES NOT TERMINATE TLS. Handler() returns an http.Handler and the composer
// serves it — because where a certificate comes from is the auth service's
// question and a gateway that owned an ACME client would own it twice.
//
// IT RUNS TRANSFORMERS AND IT DOES NOT ROUTE. A transformer is a function built
// against `implements: gateway-transformer`, and its world exists: the chain
// runs in shaped() before anything else sees the request, in the order declared,
// and any one of them may answer instead of forwarding. It runs on a WORKER
// rather than here, because a transformer is a workload of the same deployment
// and the node that is warm for the project is warm for its transformers.
//
// IT RUNS ROUTERS TOO, and on a worker for the same reason. A gateway whose
// `routing` is a function has no static route at all; a single route whose
// `target` is `router` picks that route's handler. Both are the same question
// and one world answers it — see destination(), which runs AFTER the chain
// because a router that saw the request first would be deciding from a body
// somebody was about to redact.
// It lives under services/v2 because v1 has a `services/gateway` and both run
// in one binary for the whole span between the first v2 service and the swap —
// the same reason services/v2/auth is where it is.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/ratelimit"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// Service is a gateway.
type Service struct {
	cfg     Config
	hosts   Hosts
	workers Workers
	buses   Buses
	admits  Admits

	projects *register.Cache[*specs.Project]
	limits   *ratelimit.Buckets
	now      func() time.Time

	// meter is where this service says what it accounted for. Nil is the
	// ordinary case; see meter.go.
	meter Meter

	// logf is where a failure the caller is not told about is said. See
	// WithLogger.
	logf func(format string, args ...any)

	// mu guards the host table: which project owns a name, and when that was
	// last asked.
	mu    sync.Mutex
	owner map[string]ownerEntry
	order []string
}

type ownerEntry struct {
	project     string
	environment string
	at          time.Time
}

// New validates and allocates. It binds nothing: Handler is what a composer
// serves, and where it serves it is the composer's.
func New(cfg Config, opts ...Option) (*Service, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}

	o := options{logf: func(string, ...any) {}}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, fmt.Errorf("gateway: option: %w", err)
		}
	}
	if o.hosts == nil {
		return nil, ErrNoHosts
	}
	if o.workers == nil {
		return nil, ErrNoWorkers
	}
	if o.source == nil {
		return nil, ErrNoSource
	}
	if o.now == nil {
		o.now = time.Now
	}

	cacheOptions := []register.CacheOption{register.WithClock(o.now)}
	if cfg.PointerTTL != 0 {
		cacheOptions = append(cacheOptions, register.WithTTL(cfg.PointerTTL))
	}
	if cfg.MaxProjects != 0 {
		cacheOptions = append(cacheOptions, register.WithMax(cfg.MaxProjects))
	}

	projects, err := register.NewCache(o.source, specs.Load, cacheOptions...)
	if err != nil {
		return nil, err
	}

	return &Service{
		cfg:      cfg,
		hosts:    o.hosts,
		workers:  o.workers,
		buses:    o.buses,
		admits:   o.admits,
		projects: projects,
		limits:   ratelimit.NewBuckets(o.now, cfg.MaxHosts),
		now:      o.now,
		meter:    o.meter,
		logf:     o.logf,
		owner:    map[string]ownerEntry{},
	}, nil
}

// Handler is what a composer serves.
func (s *Service) Handler() http.Handler { return s }

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Timeout)
	defer cancel()

	// EVERY EXIT FROM THIS FUNCTION IS ACCOUNTED, and there are seventeen of
	// them. A deferred emission is the only shape that survives one more being
	// added — which is what happened to every counter that was written as a
	// line before each `return`.
	billed := &crossing{w: w, started: s.now()}
	w = billed
	defer func() { s.emit(ctx, billed.accounted(s.now())) }()

	name := hostOf(r)
	billed.host = name
	if name == "" {
		refuse(w, http.StatusBadRequest, "no host")
		return
	}

	projectName, environment, err := s.ownerOf(ctx, name)
	if err == nil {
		billed.project, billed.environment = projectName, environment
	}
	if err != nil {
		if errors.Is(err, register.ErrNoHost) {
			refuse(w, http.StatusNotFound, "no site answers on "+name)
			return
		}
		refuse(w, http.StatusBadGateway, "the host table is unreachable")
		return
	}

	proj, _, err := s.projects.At(ctx, register.Deployment{Project: projectName, Environment: environment})
	if err != nil {
		refuse(w, statusFor(err), "this environment cannot be read")
		return
	}

	candidates := s.candidatesAt(proj, name, projectName)
	if len(candidates) == 0 {
		// The register says this project owns the name and the compiled
		// project has nothing at it — a deploy that removed the gateway but
		// whose claim has not been released, or a name in a zone this node
		// cannot complete.
		refuse(w, http.StatusNotFound, "nothing serves "+name+" in this environment")
		return
	}

	m, found, methodOnly := pick(candidates, r.URL.Path, r.Method)
	if !found {
		// A preflight is answered from the name's policy even when the method
		// it asks about has no route, because that is a question about what is
		// allowed and not a request for a page.
		if r.Method == http.MethodOptions {
			cors(w, r, candidates[0].gateway.Cors, name)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if methodOnly {
			refuse(w, http.StatusMethodNotAllowed, "that method is not served here")
			return
		}
		refuse(w, http.StatusNotFound, "no route matches "+r.URL.Path)
		return
	}

	cors(w, r, m.gateway.Cors, name)

	if limit := m.gateway.RateLimit; limit.Rpm != nil && *limit.Rpm > 0 {
		rpm, burst := *limit.Rpm, int64(0)
		if limit.Burst != nil {
			burst = *limit.Burst
		}
		key := limitKey(limit.Key, r)
		if !s.limits.Allow(name+"\x00"+key, int(rpm), int(burst)) {
			w.Header().Set("Retry-After", retryAfter(int(rpm)))
			refuse(w, http.StatusTooManyRequests, "too many requests")
			return
		}
	}

	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if code, why := s.unbuilt(m); code != 0 {
		refuse(w, code, why)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBody))
	billed.in = int64(len(body))
	if err != nil {
		refuse(w, http.StatusRequestEntityTooLarge, "the request body is too large")
		return
	}

	req := &invoke.Request{
		Method:  r.Method,
		Path:    m.stripped(r.URL.Path),
		Headers: r.Header,
		Body:    body,
	}

	// THE GUARD RUNS BEFORE THE CHAIN, because a transformer shapes traffic and
	// a guard decides whether there is traffic to shape. Running it after would
	// hand somebody's filter a request the policy had not admitted, and would
	// make the refusal depend on what the chain did to it first.
	if code, why := s.admitted(ctx, m, projectName, environment, req); why != "" {
		refuse(w, code, why)
		return
	}

	// THE CHAIN RUNS BEFORE ANYTHING ELSE SEES THE REQUEST, in the order it was
	// declared, and any one of them may stop it.
	if answer, code, why := s.shaped(ctx, m, projectName, environment, req); why != "" {
		refuse(w, code, why)
		return
	} else if answer != nil {
		// A TRANSFORMER ANSWERED, which is a served request: somebody's code ran
		// and produced this. A filter that rejects is doing its job, not
		// failing.
		billed.served = true
		writeTo(w, answer)
		return
	}

	// A BUS IS ATTACHED TO RATHER THAN INVOKED, and it happens HERE -- after the
	// transformer chain and before the router -- because that is what routing a
	// bus was for. It used to own a `host:` and be found by walking the hosts
	// index, which skipped the path, the methods, the CORS, the rate limit and
	// the chain: a bus could not be authorized or rate-limited at all. Reached as
	// a handler, it gets every one of them for the price of being named.
	if m.handler().HandlerKind == busKind {
		s.attachRouted(w, r, billed, m, proj, projectName, environment)
		return
	}

	// WHERE IT GOES, which is the route's own answer unless a function decides.
	// The router runs AFTER the chain: one that saw the request first would be
	// deciding from a body somebody was about to redact.
	at, answer, code, why := s.destination(ctx, m, projectName, environment, req)
	if why != "" {
		refuse(w, code, why)
		return
	}
	if answer != nil {
		billed.served = true
		writeTo(w, answer)
		return
	}

	// A PORT IS THE ROUTE'S, so it applies where the route's own handler is
	// the destination — a router that chose somewhere else chose a target and
	// not a socket on this one.
	var port string
	if router, _ := m.router(); router == "" {
		port = m.handler().HandlerPort
	}
	res, err := s.deliver(ctx, at, port, req)
	if err != nil {
		s.logf("gateway: %s/%s %s/%s could not be served: %v", at.Project, at.Environment, at.Kind, at.Name, err)
		refuse(w, statusFor(err), "this route could not be served")
		return
	}

	// A WORKLOAD ANSWERED, which is what separates a served request from every
	// refusal this handler makes on its own — and the refusals are the half no
	// invocation count can show.
	billed.served = true

	writeTo(w, res)
}

// writeTo sends one invocation's answer, whoever produced it.
func writeTo(w http.ResponseWriter, res *invoke.Response) {
	for key, values := range res.Headers {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	status := res.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	w.Write(res.Body)
}

// destination is where this request goes: the route's declared handler, or
// whatever its router decided.
//
// A ROUTER IS NAMED IN TWO PLACES and they are the same question. A gateway
// whose `routing` is `router` has no static routes at all; a single route whose
// `target` is `router` picks that route's handler. routerAttributes in the DSL
// is one function for exactly that reason, and this is its other half.
//
// A ROUTER THAT COULD NOT BE RUN IS NOT A 404. A routing table that cannot be
// read is not the same as a path that is not in it — one is this cloud's
// problem and the other is the client's, and telling a client (and every cache
// between here and it) that a path does not exist is not a thing to guess at.
//
// It answers (address, nil, 0, "") to invoke, (_, answer, 0, "") to serve that
// response, and (_, nil, code, why) to refuse.
// admitted asks the route's guard, or the gateway's, whether this request may
// pass. An empty reason is a pass.
//
// A ROUTE'S OWN ANSWERS INSTEAD OF THE GATEWAY'S, which is what having both
// sites is for: a gateway states the policy its routes inherit, and a route
// that needs its own says so where the route is written.
//
// WHAT A REFUSAL SAYS IS DELIBERATELY LITTLE. The guard's reason is for an
// operator — a refusal that repeated it would describe somebody's policy to
// anyone who asked, which is the rule `refuse` keeps everywhere else.
func (s *Service) admitted(ctx context.Context, m match, projectName, environment string, req *invoke.Request) (int, string) {
	name, scope := m.guard()
	if name == "" {
		return 0, ""
	}
	if s.admits == nil {
		// A composition fault rather than a deployment one: the project says
		// this name is guarded and this node cannot run the guard. Serving it
		// would be serving exactly what the author said to decide about.
		return http.StatusNotImplemented, "this route is guarded and that is not available here"
	}
	if scope == nil {
		scope = m.host.Scope
	}

	admission, err := s.admits.AdmitRequest(ctx, specs.Address{
		Project:     projectName,
		Environment: environment,
		Scope:       scope,
		Kind:        specs.KindFunction,
		Name:        name,
	}, req)
	if err != nil {
		return http.StatusServiceUnavailable, "the function that guards this name could not be run"
	}
	if admission.Denied != "" {
		return http.StatusForbidden, "this request was not admitted"
	}
	if len(admission.Granted) == 0 {
		// A GRANT OF NOTHING IS A DECIDED REFUSAL, and it is the commonest one
		// this world has — not an error, and not a pass.
		return http.StatusForbidden, "this request was not admitted"
	}
	return 0, ""
}

func (s *Service) destination(ctx context.Context, m match, projectName, environment string, req *invoke.Request) (specs.Address, *invoke.Response, int, string) {
	router, scope := m.router()
	if router == "" {
		return m.address(projectName, environment), nil, 0, ""
	}

	if scope == nil {
		scope = m.host.Scope
	}
	out, err := s.workers.Route(ctx, specs.Address{
		Project:     projectName,
		Environment: environment,
		Scope:       scope,
		Kind:        specs.KindFunction,
		Name:        router,
	}, req)
	if err != nil {
		s.logf("gateway: %s/%s router %s could not be run: %v", projectName, environment, router, err)
		return specs.Address{}, nil, http.StatusServiceUnavailable,
			"the function that routes this name could not be run"
	}

	switch {
	case out.Answer != nil:
		return specs.Address{}, out.Answer, 0, ""

	case out.None != "":
		// THE ROUTER RAN AND SAID NOTHING HANDLES THIS, which is the one case
		// where a 404 is the truth rather than a guess.
		return specs.Address{}, nil, http.StatusNotFound, "nothing here handles that"

	case out.To != nil:
		to := m.host.Scope
		if out.To.Namespace != "" {
			to = splitScope(out.To.Namespace)
		}
		return specs.Address{
			Project:     projectName,
			Environment: environment,
			Scope:       to,
			Kind:        out.To.Kind,
			Name:        out.To.Name,
		}, nil, 0, ""
	}

	return specs.Address{}, nil, http.StatusBadGateway, "that router decided nothing"
}

// shaped runs this route's transformers, in order, over req.
//
// IT MUTATES req IN PLACE as each one forwards, because the chain is a pipeline:
// what the second transformer sees is what the first produced, and what the
// handler sees is what the last one did. A chain that handed each transformer
// the ORIGINAL request would be a list of independent opinions, none of which
// could build on another.
//
// WHAT HAPPENS WHEN ONE FAILS IS THE AUTHOR'S, per entry, and it is the one
// decision here that is genuinely dangerous in both directions: a filter that
// fails open stops filtering exactly when something is wrong, and an enrichment
// that fails closed takes the site down to add a header. So `fail` is required
// to be one of two words and neither is guessed.
//
// It answers (answer, 0, "") to stop the chain with that response, (nil, 0, "")
// to carry on, and (nil, code, why) to refuse.
func (s *Service) shaped(ctx context.Context, m match, projectName, environment string, req *invoke.Request) (*invoke.Response, int, string) {
	for _, t := range m.gateway.Transformers {
		name := t.Name
		if name == "" {
			continue
		}

		scope := m.host.Scope
		if declared := t.Namespace; declared != "" {
			scope = splitScope(declared)
		}

		out, err := s.workers.Transform(ctx, specs.Address{
			Project:     projectName,
			Environment: environment,
			Scope:       scope,
			Kind:        specs.KindFunction,
			Name:        name,
		}, req)
		if err != nil {
			s.logf("gateway: %s/%s transformer %s could not be run: %v", projectName, environment, name, err)
			if t.Fail == specs.TransformersEntryFailOpen {
				// The author said the request is better served without it.
				continue
			}
			return nil, http.StatusServiceUnavailable,
				"a transformer this route requires could not be run, and it is declared fail-closed"
		}

		switch {
		case out.Answer != nil:
			return out.Answer, 0, ""
		case out.Forward != nil:
			*req = *out.Forward
		}
	}
	return nil, 0, ""
}

// unbuilt reports the parts of the vocabulary this gateway cannot yet honour,
// and says so rather than serving a request as if they were not there.
func (s *Service) unbuilt(m match) (int, string) {

	// TRANSFORMERS ARE NOT HERE ANY MORE. They ran nowhere, so a fail-closed one
	// had to refuse the route and a fail-open one had to be skipped; now they
	// run, in order, in shaped(). What a failing one means is still the author's
	// per entry, and it is decided there — where the failure is — rather than
	// here, where the only thing knowable was that nothing could run at all.
	return 0, ""
}

// candidatesAt is every gateway resource in this project answering on a name.
func (s *Service) candidatesAt(p *specs.Project, name, projectName string) []candidate {
	var out []candidate
	for _, h := range p.At(s.lookupName(name)) {
		if h.Kind != gatewayKind {
			continue
		}
		r, _, err := p.Resource(h.Scope, h.Kind, h.Name)
		if err != nil {
			continue
		}
		out = append(out, candidate{host: h, gateway: specs.ReadGateway(r)})
	}
	return out
}

// gatewayKind is what an HTTP entry point is called in the vocabulary. A proxy
// answers on a name too, and is not one of these: it carries a raw connection
// and never a request, so nothing here can parse it.
const gatewayKind = specs.KindGateway

// ownerOf says whose name this is, remembering the answer for HostTTL.
func (s *Service) ownerOf(ctx context.Context, name string) (string, string, error) {
	s.mu.Lock()
	entry, ok := s.owner[name]
	s.mu.Unlock()

	if ok && s.now().Sub(entry.at) < s.cfg.HostTTL {
		return entry.project, entry.environment, nil
	}

	projectName, environment, err := s.hosts.ResolveHost(ctx, name)
	if err != nil {
		return "", "", err
	}

	s.mu.Lock()
	if _, held := s.owner[name]; !held {
		s.order = append(s.order, name)
		for len(s.order) > s.cfg.MaxHosts {
			delete(s.owner, s.order[0])
			s.order = s.order[1:]
		}
	}
	s.owner[name] = ownerEntry{project: projectName, environment: environment, at: s.now()}
	s.mu.Unlock()

	return projectName, environment, nil
}

// hostOf is the name the request was made to, without its port and lowercased.
//
// DNS IS CASE-INSENSITIVE AND A Host HEADER CARRIES WHAT THE CLIENT TYPED, so a
// table keyed by what the author wrote would miss half the requests for one
// name.
func hostOf(r *http.Request) string {
	name := r.Host
	if r.URL != nil && r.URL.Host != "" {
		name = r.URL.Host
	}
	if i := strings.LastIndex(name, ":"); i > 0 && !strings.Contains(name[i:], "]") {
		name = name[:i]
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}

// statusFor turns a failure from below into what the client is told.
//
// THE DISTINCTION IS WHOSE PROBLEM IT IS. A deployment missing a piece — no
// tier, no artifact, no secret — is a 502: the site is broken and the caller
// can do nothing about it. A node at capacity is a 503 with the request worth
// retrying. Saying 404 for either would send somebody looking for a typo.
func statusFor(err error) int {
	switch {
	case errors.Is(err, register.ErrNotPublished), errors.Is(err, specs.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.Is(err, invoke.ErrAtCapacity):
		return http.StatusServiceUnavailable
	}

	switch connect.CodeOf(err) {
	case connect.CodeNotFound:
		return http.StatusNotFound
	case connect.CodeResourceExhausted:
		return http.StatusServiceUnavailable
	case connect.CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}

// refuse is the one place a body is written for a failure, so what a client
// sees is decided once.
//
// IT SAYS NOTHING ABOUT THE PROJECT. A 404 that named the environment, the
// resource it looked for or the reason the register refused would be a public
// listener describing somebody's deployment to anyone who asked.
func refuse(w http.ResponseWriter, status int, why string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintln(w, why)
}

// Held is how many compiled projects and names are cached. It exists for a test
// and for an operator asking what a node is holding.
func (s *Service) Held() (projects, names int) {
	s.mu.Lock()
	names = len(s.owner)
	s.mu.Unlock()
	return s.projects.Held(), names
}
