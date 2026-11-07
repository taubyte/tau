// Package gateway is the way in: the only v2 service facing the internet, turning a
// request into a target.
//
//	a Host header      -> whose environment this is        [the register]
//	that environment   -> what it currently runs           [register.Cache]
//	the name and path  -> which gateway resource serves it [the compiled config]
//	the route's handler-> a worker, over p2p
//
// Every policy (CORS, rate limits, methods, path matching, prefix stripping) comes
// from the config; an operator declares only the cloud's zone and node budgets. It
// chooses a route, never a node (Workers decides from live measurements).
//
// It binds no socket: Handler is served by the composer, which asks Certificate per
// name for TLS (tls.go). Transformers run in shaped(), in declared order, before
// anything sees the request, each able to answer; routers run in destination(), after
// the chain. Both run on a worker, where the project is warm.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"slices"
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

	// secrets reads a provided certificate, and terminates says the composer
	// serves this handler over TLS too. See tls.go.
	secrets    Secrets
	issuer     Issuer
	terminates bool

	// artifacts and assets are where a package handler's files come from, and files
	// what is unpacked of them. See files.go.
	artifacts Artifacts
	assets    Assets
	files     *unpacked

	// answers is what routes that say `cache:` keep. See cache.go.
	answers *answers

	// cands memoises candidatesAt, a candKey's []candidate: a compiled project never
	// changes once loaded. The candidates are shared, so nothing may write through
	// them. A request reads it without a lock; candMu orders the writes and guards
	// candProjects, the projects it holds, which bounds it.
	cands        sync.Map
	candMu       sync.Mutex
	candProjects map[*specs.Project]struct{}
}

// candKey is a name within one compiled project.
type candKey struct {
	project *specs.Project
	name    string
}

// maxCandidateProjects bounds cands; past it the table starts over.
const maxCandidateProjects = 256

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

	var cacheOptions []register.CacheOption
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

		secrets:    o.secrets,
		issuer:     o.issuer,
		terminates: o.terminates,

		artifacts: o.artifacts,
		assets:    o.assets,
		files:     newUnpacked(cfg.FilesBudget),
		answers:   newAnswers(cfg.CacheBudget),
	}, nil
}

// Handler is what a composer serves.
func (s *Service) Handler() http.Handler { return s }

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A CA PROVING A NAME is answered before any name is looked up: the
	// answer is the account's, and the name may not be published yet on the
	// node the CA reached.
	if token, ok := strings.CutPrefix(r.URL.Path, acmeChallengePrefix); ok && s.issuer != nil {
		answer, ok := s.issuer.Challenge(token)
		if !ok {
			// NOT A 404: the answer may not have reached this node yet, and a
			// CA reading "not found" fails the order.
			http.Error(w, "no answer for that token here yet", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, answer)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Timeout)
	defer cancel()

	// A deferred emission accounts every exit, including ones added later.
	x := &exchange{crossing: crossing{w: w, started: s.now()}}
	billed := &x.crossing
	w = billed
	defer func() { s.emit(ctx, billed.accounted(s.now())) }()

	name := hostOf(r)
	billed.host = name
	if name == "" {
		refuse(w, http.StatusBadRequest, "no host")
		return
	}

	projectName, environment, err := s.hosts.ResolveHost(ctx, name)
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

	proj, release, err := s.projects.At(ctx, register.Deployment{Project: projectName, Environment: environment})
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

	if why := s.cleartext(m, r); why != "" {
		refuse(w, http.StatusMisdirectedRequest, why)
		return
	}

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

	body, err := readBody(w, r, s.cfg.MaxBody)
	billed.in = int64(len(body))
	if err != nil {
		refuse(w, http.StatusRequestEntityTooLarge, "the request body is too large")
		return
	}

	req := &x.req
	*req = invoke.Request{
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

	// A bus is attached here, after the chain and before the router, so it gets the
	// route's path, methods, CORS, rate limit and chain like any handler.
	if m.handler().HandlerKind == busKind {
		s.attachRouted(w, r, billed, m, proj, projectName, environment)
		return
	}

	// A KEPT ANSWER is served after the guard and the chain, which decide per request,
	// and before anything that would ask the handler.
	key, cached := cacheKey(m, release.Object, name, r, req)
	if cached {
		if k, ok := s.answers.get(key, s.now()); ok {
			billed.served = true
			k.served(w, s.now())
			return
		}
	}

	// A package's files are read here rather than run: the same place in the order,
	// and nothing is invoked.
	if m.handler().HandlerKind == packageKind {
		res, code, why := s.serveFiles(ctx, m, proj, projectName, environment, req)
		if why != "" {
			refuse(w, code, why)
			return
		}
		billed.served = true
		if cached {
			s.keep(w, key, routeTtl(m.gateway), res)
		}
		writeTo(w, res)
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

	if cached {
		s.keep(w, key, routeTtl(m.gateway), res)
	}
	writeTo(w, res)
}

// writeTo sends one invocation's answer, whoever produced it.
func writeTo(w http.ResponseWriter, res *invoke.Response) {
	// A response's values are its own and are handed over rather than copied a
	// value at a time, clipped so a later Add copies instead of writing past them.
	h := w.Header()
	for key, values := range res.Headers {
		if len(values) == 0 {
			continue
		}
		k := textproto.CanonicalMIMEHeaderKey(key)
		if held, ok := h[k]; ok {
			h[k] = append(held, values...)
		} else {
			h[k] = slices.Clip(values)
		}
	}
	status := res.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	w.Write(res.Body)
}

// admitted asks the route's guard, or the gateway's, whether this request may pass;
// an empty reason passes. A route's own guard overrides the gateway's. A refusal says
// little: the guard's reason is for the operator.
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

// destination is where this request goes: the route's declared handler, or
// whatever its router decided. A router that could not be run is not a 404: an
// unreadable routing table is this cloud's problem, not a missing path.
//
// It answers (address, nil, 0, "") to invoke, (_, answer, 0, "") to serve that
// response, and (_, nil, code, why) to refuse.
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
// It mutates req in place as each one forwards: the chain is a pipeline, so each
// transformer sees what the previous one produced.
//
// What a failure means is the author's, per entry: a filter that fails open stops
// filtering when something is wrong, and an enrichment that fails closed takes the
// site down to add a header. So `fail` must be one of two words, never guessed.
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
// rather than serving a request as if they were not there.
func (s *Service) unbuilt(m match) (int, string) {
	return 0, ""
}

// candidatesAt is every gateway resource in this project answering on a name.
func (s *Service) candidatesAt(p *specs.Project, name, projectName string) []candidate {
	lookup := s.lookupName(name)

	k := candKey{project: p, name: lookup}
	if held, ok := s.cands.Load(k); ok {
		return held.([]candidate)
	}

	out := readCandidates(p, lookup)

	s.candMu.Lock()
	if _, ok := s.candProjects[p]; !ok {
		if s.candProjects == nil || len(s.candProjects) >= maxCandidateProjects {
			s.cands.Clear()
			s.candProjects = map[*specs.Project]struct{}{}
		}
		s.candProjects[p] = struct{}{}
	}
	s.cands.Store(k, out)
	s.candMu.Unlock()
	return out
}

func readCandidates(p *specs.Project, lookup string) []candidate {
	var out []candidate
	for _, h := range p.At(lookup) {
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

// gatewayKind is an HTTP entry point. A proxy answers on a name too, but carries a
// raw connection, never a request.
const gatewayKind = specs.KindGateway

// exchange is one request's metering and, once it reaches a workload, what it hands
// one: a single allocation for both.
type exchange struct {
	crossing crossing
	req      invoke.Request
}

// readBody reads r's body whole, at most limit bytes. A request declaring none reads
// nothing, one declaring its length is read into exactly that, and one declaring
// more than limit is refused unread. Only a body of unknown length grows a buffer.
func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	switch {
	case r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0:
		return nil, nil
	case r.ContentLength > limit:
		return nil, &http.MaxBytesError{Limit: limit}
	case r.ContentLength > 0:
		body := make([]byte, r.ContentLength)
		n, err := io.ReadFull(http.MaxBytesReader(w, r.Body, limit), body)
		return body[:n], err
	}
	return io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
}

// hostOf is the name the request was made to, without its port and lowercased,
// since DNS is case-insensitive and a Host header carries what the client typed.
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

// statusFor turns a failure from below into what the client is told. A deployment
// missing a piece (tier, artifact, secret) is a 502: the caller can do nothing. A
// node at capacity is a 503, worth retrying. A 404 would send somebody looking for a
// typo.
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

// refuse is the one place a failure body is written. It says nothing about the
// project: a public listener must not describe somebody's deployment.
func refuse(w http.ResponseWriter, status int, why string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintln(w, why)
}

// Held is how many compiled projects are cached. It exists for a test and for an
// operator asking what a node is holding. Names are kept by the register reader the
// gateway resolves them through, not here.
func (s *Service) Held() int { return s.projects.Held() }
