package worker

import (
	"context"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// The one traversal for what this node can fire, drain, and accept connections for.
// The register does not enumerate, so it covers only deployments already served. A
// scope is not only a namespace: a workflow holds the queues and drivers it lowered
// to.

// eachDeployment calls visit once per (project, environment) this node holds. A held
// pointer whose object cannot be read is skipped; the next pass retries.
func (s *Service) eachDeployment(ctx context.Context, visit func(proj *specs.Project, name, environment string)) {
	for _, at := range s.projects.Deployments() {
		proj, _, err := s.projects.At(ctx, at)
		if err != nil {
			continue
		}
		visit(proj, at.Project, at.Environment)
	}
}

// eachScope calls visit for the given scope and then every scope within it,
// including those a lowered kind produced.
func eachScope(proj *specs.Project, scope []string, visit func(scope []string)) {
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
const proxyKind = specs.KindProxy

// Proxy is one proxy resource in a deployment this node holds, addressed the
// way services/proxy takes one.
type Proxy struct {
	Project     string
	Environment string

	// Scope is the namespace chain holding it, outermost first.
	Scope []string
	Name  string
}

// Proxies is every proxy in the deployments this node holds. Every holding node
// serves it, with no claim or leader, so an SRV answer has several targets.
func (s *Service) Proxies(ctx context.Context) []Proxy {
	var out []Proxy
	s.eachDeployment(ctx, func(proj *specs.Project, name, environment string) {
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

// At is the compiled project for one deployment, from this node's cache, exposed so
// the proxy does not keep a second cache that could disagree about the revision.
func (s *Service) At(ctx context.Context, at register.Deployment) (*specs.Project, register.Pointer, error) {
	return s.projects.At(ctx, at)
}
