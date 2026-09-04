// Package gateway is the way in. It is the only v2 service that faces the
// internet, and the only one that turns a request into a target.
//
// # What it does, per request
//
//	a Host header      -> whose environment this is        [the register]
//	that environment   -> what it currently runs           [project.Cache]
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
// IT DOES NOT RUN TRANSFORMERS OR ROUTER FUNCTIONS. Both are functions built
// against an interface (`implements: gateway-transformer`, `gateway-router`)
// whose ABI is not defined anywhere yet. Rather than pretend, a gateway that
// declares either says so — and for a transformer it says so the way the
// author asked: `fail: closed` refuses the request, `fail: open` serves it
// without. A `redact` transformer that strips card numbers is declared closed
// for exactly this reason, and running its route without it would be the
// gateway deciding a security control was optional.
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
	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/register"
)

// Service is a gateway.
type Service struct {
	cfg     Config
	hosts   Hosts
	workers Workers

	projects *project.Cache
	limits   *limiter
	now      func() time.Time

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

	var o options
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

	cacheOptions := []project.CacheOption{project.WithClock(o.now)}
	if cfg.PointerTTL != 0 {
		cacheOptions = append(cacheOptions, project.WithTTL(cfg.PointerTTL))
	}
	if cfg.MaxProjects != 0 {
		cacheOptions = append(cacheOptions, project.WithMax(cfg.MaxProjects))
	}

	projects, err := project.NewCache(o.source, cacheOptions...)
	if err != nil {
		return nil, err
	}

	return &Service{
		cfg:      cfg,
		hosts:    o.hosts,
		workers:  o.workers,
		projects: projects,
		limits:   newLimiter(o.now, cfg.MaxHosts),
		now:      o.now,
		owner:    map[string]ownerEntry{},
	}, nil
}

// Handler is what a composer serves.
func (s *Service) Handler() http.Handler { return s }

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Timeout)
	defer cancel()

	name := hostOf(r)
	if name == "" {
		refuse(w, http.StatusBadRequest, "no host")
		return
	}

	projectName, environment, err := s.ownerOf(ctx, name)
	if err != nil {
		if errors.Is(err, register.ErrNoHost) {
			refuse(w, http.StatusNotFound, "no site answers on "+name)
			return
		}
		refuse(w, http.StatusBadGateway, "the host table is unreachable")
		return
	}

	proj, _, err := s.projects.At(ctx, projectName, environment)
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
			cors(w, r, candidates[0].resource, name)
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

	cors(w, r, m.resource, name)

	if rpm, ok := m.resource.Int("rate_limit", "rpm"); ok && rpm > 0 {
		burst, _ := m.resource.Int("rate_limit", "burst")
		key := limitKey(m.resource.String("rate_limit", "key"), r)
		if !s.limits.allow(name+"\x00"+key, int(rpm), int(burst)) {
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
	if err != nil {
		refuse(w, http.StatusRequestEntityTooLarge, "the request body is too large")
		return
	}

	res, err := s.workers.Invoke(ctx, m.address(projectName, environment), &invoke.Request{
		Method:  r.Method,
		Path:    m.stripped(r.URL.Path),
		Headers: r.Header,
		Body:    body,
	})
	if err != nil {
		refuse(w, statusFor(err), "this route could not be served")
		return
	}

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

// unbuilt reports the parts of the vocabulary this gateway cannot yet honour,
// and says so rather than serving a request as if they were not there.
func (s *Service) unbuilt(m match) (int, string) {
	// A gateway whose routing is a function has no static route at all, so a
	// request reaching it has nowhere to go until that function's interface
	// exists.
	if m.resource.String("routing") == "router" {
		return http.StatusNotImplemented, "this name is routed by a function, which is not implemented"
	}

	// TRANSFORMERS ARE A LIST AND NOT A MAP, because the order they run in is
	// what they mean. Reading them with Keys returns nothing and drops every
	// entry — which for a fail-closed one is a security control that quietly
	// stops existing.
	for _, t := range m.resource.List("transformers") {
		if t.String("fail") == "open" {
			// The author said the request is better served without it.
			continue
		}
		return http.StatusServiceUnavailable,
			"a transformer this route requires is not implemented, and it is declared fail-closed"
	}
	return 0, ""
}

// candidatesAt is every gateway resource in this project answering on a name.
//
// A GENERATED NAME LOSES THE CLOUD'S SEGMENT FIRST. The compiled project
// carries `<label>.<project label>` because the cloud's own fqdn is not in the
// config; this is the node that knows it, so it takes it off the request rather
// than putting it into the project.
func (s *Service) candidatesAt(p *project.Project, name, projectName string) []candidate {
	lookup := strings.ToLower(name)
	if s.cfg.CloudFqdn != "" {
		if trimmed, ok := strings.CutSuffix(lookup, "."+strings.ToLower(s.cfg.CloudFqdn)); ok {
			lookup = trimmed
		}
	}

	var out []candidate
	for _, h := range p.At(lookup) {
		if h.Kind != gatewayKind {
			continue
		}
		r, _, err := p.Resource(h.Scope, h.Kind, h.Name)
		if err != nil {
			continue
		}
		out = append(out, candidate{host: h, resource: r})
	}
	return out
}

// gatewayKind is what an HTTP entry point is called in the vocabulary. A proxy
// answers on a name too, and is not one of these: it carries a raw connection
// and never a request, so nothing here can parse it.
const gatewayKind = "gateway"

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
	case errors.Is(err, register.ErrNotPublished), errors.Is(err, project.ErrNotFound):
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
