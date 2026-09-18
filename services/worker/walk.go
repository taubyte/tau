package worker

import (
	"context"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/register"
)

// walk.go is the one traversal three questions are asked through: what this
// node can fire, what it can drain, and what it can accept a connection for.
//
// THE LAZINESS IS THE SAME EVERY TIME, and it is not an optimisation. The
// register does not enumerate, so what a node can answer for is what it is
// ALREADY SERVING — a deployment nothing has touched is invisible here, and
// every caller has to be able to say what that costs it. See Schedules, which
// carries the sharpest case.
//
// It was written out three times before it was written down once, and the
// recursion was the part that kept being got right by copying: a scope is not
// only a namespace, so a walk asking for namespaces walks straight past every
// queue and driver a workflow lowered to.

// eachDeployment calls visit once per (project, environment) this node holds.
//
// A DEPLOYMENT WHOSE POINTER IS HELD AND WHOSE OBJECT IS NOT IS SKIPPED. It
// moved under us, and the next pass is the retry — there is nothing to report
// about a project this node cannot read.
func (s *Service) eachDeployment(ctx context.Context, visit func(proj *project.Project, name, environment string)) {
	for _, held := range s.projects.Environments() {
		name, environment, ok := splitDeployment(held)
		if !ok {
			continue
		}
		proj, _, err := s.projects.At(ctx, name, environment)
		if err != nil {
			continue
		}
		visit(proj, name, environment)
	}
}

// eachScope calls visit for the given scope and then for every scope within it.
//
// EVERY SCOPE AND NOT ONLY EVERY NAMESPACE. A kind that lowers becomes a
// container of what it produced — a workflow holds its steps' queues and
// drivers — so a walk that asked for namespaces missed all of them.
func eachScope(proj *project.Project, scope []string, visit func(scope []string)) {
	visit(scope)

	scopes, err := proj.Scopes(scope)
	if err != nil {
		return
	}
	for _, namespace := range scopes {
		eachScope(proj, append(append([]string{}, scope...), namespace), visit)
	}
}

// proxyKind is the vocabulary's name for a raw-connection listener.
const proxyKind = "proxy"

// Proxy is one proxy resource in a deployment this node holds, addressed the
// way services/v2/proxy takes one.
type Proxy struct {
	Project     string
	Environment string

	// Scope is the namespace chain holding it, outermost first.
	Scope []string
	Name  string
}

// Proxies is every proxy in the deployments this node already holds.
//
// A PROXY IS SERVED BY EVERY NODE THAT HOLDS IT, unlike a cron. There is no
// claim and no leader here: a listener on each node is what makes an SRV answer
// have more than one target, and losing a node then costs a client one retry
// rather than the service.
func (s *Service) Proxies(ctx context.Context) []Proxy {
	var out []Proxy
	s.eachDeployment(ctx, func(proj *project.Project, name, environment string) {
		eachScope(proj, nil, func(scope []string) {
			names, err := proj.Names(scope, proxyKind)
			if err != nil {
				return
			}
			for _, proxy := range names {
				out = append(out, Proxy{
					Project: name, Environment: environment,
					Scope: append([]string(nil), scope...), Name: proxy,
				})
			}
		})
	})

	sort.Slice(out, func(i, j int) bool { return out[i].where() < out[j].where() })
	return out
}

func (p Proxy) where() string {
	parts := make([]string, 0, len(p.Scope)+3)
	parts = append(parts, p.Project, p.Environment)
	parts = append(parts, p.Scope...)
	return strings.Join(append(parts, p.Name), "/")
}

// At is the compiled project for one deployment, out of the cache this node
// already holds.
//
// IT IS EXPOSED FOR THE PROXY, which serves the proxies of the deployments this
// worker holds and would otherwise keep a second cache of the same objects —
// two caches that can disagree about which revision of one project is current,
// which for a listener means forwarding to a workload the config no longer
// names.
func (s *Service) At(ctx context.Context, name, environment string) (*project.Project, register.Pointer, error) {
	return s.projects.At(ctx, name, environment)
}
