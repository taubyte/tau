// Package zone answers DNS for the zones a project declares: an adapter between the
// DNS server and the worker and component tier. Both arms, the records an author
// wrote and the resolver function they named, share one walk up the labels to the
// covering zone.
package zone

import (
	"context"
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/worker"
)

// Hosts maps a name somebody owns to the deployment that answers for it.
type Hosts interface {
	ResolveHost(ctx context.Context, fqdn string) (project string, environment string, err error)
}

// Projects reads compiled projects and finds the functions in them.
//
// TWO METHODS AND NOT THE WORKER, because those two are all a zone needs: the
// compiled object to read its zones from, and a resolver's spec to invoke. A
// parameter typed as the worker would carry Invoke, Transform, Route, Direct,
// Shape and Conn along with it, none of which a DNS answer has any business
// reaching.
type Projects interface {
	At(ctx context.Context, at register.Deployment) (*specs.Project, register.Pointer, error)
	Resolve(ctx context.Context, t worker.Target) (invoke.Spec, error)
}

// Guests instantiates a component and asks it one question.
type Guests interface {
	Resolve(ctx context.Context, spec invoke.Spec, q invoke.Question) (*invoke.Resolution, error)
}

// Zones is the adapter. Construct it with New.
type Zones struct {
	hosts    Hosts
	projects Projects
	guests   Guests
}

// New wires the adapter.
//
// guests MAY BE NIL, and that is the node that runs no component tier: its zones
// answer what was written down and nothing is asked. hosts and projects may not
// be, because without either there is no way to find a zone at all.
func New(hosts Hosts, projects Projects, guests Guests) (*Zones, error) {
	if hosts == nil {
		return nil, fmt.Errorf("zone: no host index, so no name can be mapped to a project")
	}
	if projects == nil {
		return nil, fmt.Errorf("zone: no projects, so no zone can be read")
	}
	return &Zones{hosts: hosts, projects: projects, guests: guests}, nil
}

// covered is the zone that answers for a name, and the deployment it came from.
type covered struct {
	zone        specs.Zone
	project     string
	environment string
}

// covering walks a query name up its labels to the zone that declares it, most
// specific first, stopping at two labels so a miss does not query every public
// suffix. A miss costs one index lookup per label.
func (z *Zones) covering(ctx context.Context, name string) (*covered, error) {
	labels := strings.Split(strings.ToLower(strings.TrimSuffix(name, ".")), ".")

	for i := 0; i+1 < len(labels); i++ {
		apex := strings.Join(labels[i:], ".")

		projectName, environment, err := z.hosts.ResolveHost(ctx, apex)
		if err != nil {
			continue
		}

		proj, _, err := z.projects.At(ctx, register.Deployment{Project: projectName, Environment: environment})
		if err != nil {
			// The index says this project owns the apex and its compiled form
			// is not readable here. The next label up is not the answer — this
			// one is, and it is unavailable — so the walk stops.
			return nil, fmt.Errorf("zone: reading %s/%s for %s: %w", projectName, environment, apex, err)
		}

		for _, declared := range proj.Zones() {
			if declared.Fqdn == apex {
				return &covered{zone: declared, project: projectName, environment: environment}, nil
			}
		}

		// THE INDEX MATCHED AND THE PROJECT DECLARES NO SUCH ZONE. That is a
		// gateway host at this name rather than a zone apex, so the walk
		// carries on: a project may serve `shop.example.com` from a gateway
		// and still declare `example.com` as a zone.
	}
	return nil, nil
}

// Records is the arm an author wrote down.
func (z *Zones) Records(ctx context.Context, name string) (string, []string, error) {
	at, err := z.covering(ctx, name)
	if err != nil || at == nil {
		return "", nil, err
	}
	return at.zone.Fqdn, at.zone.Records, nil
}

// Resolve asks the zone's own function about one name.
//
// NIL WITH NO ERROR IS "NOBODY WAS ASKED", and there are three ways to get
// there: no zone covers the name, the zone named no resolver, or this node runs
// no component tier. None of them is a statement about the name, so the server
// carries on to whatever else it can do — a zone with a resolver it cannot run
// must not start answering NXDOMAIN.
func (z *Zones) Resolve(ctx context.Context, q invoke.Question) (*invoke.Resolution, error) {
	at, err := z.covering(ctx, q.Name)
	if err != nil || at == nil {
		return nil, err
	}
	if at.zone.Resolver.Name == "" {
		return nil, nil
	}
	if z.guests == nil {
		return nil, nil
	}

	// The resolver's own spec, resolved like any function. The asker is nobody: the
	// zone's namespace arrives absolute (pkg/specs).
	resolver, err := z.projects.Resolve(ctx, worker.Target{
		Project:     at.project,
		Environment: at.environment,
		Scope:       specs.ScopeOf(nil, at.zone.Resolver.Namespace),
		Kind:        specs.KindFunction,
		Name:        at.zone.Resolver.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("zone: finding resolver %q for %s: %w", at.zone.Resolver.Name, at.zone.Fqdn, err)
	}

	return z.guests.Resolve(ctx, resolver, q)
}
