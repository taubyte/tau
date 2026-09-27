// Package zone answers DNS for the zones a project declares.
//
// # Why it is its own package
//
// It is an ADAPTER: it sits between the DNS server, which knows nothing about
// projects, and the worker and component tier, which know nothing about DNS. The
// composition root is the only place that may NAME all three — but naming them
// and implementing the adapter are different jobs, and conflating them is how
// node/ grew to 8,000 lines with twenty-two of these in one namespace.
//
// So the root constructs this and hands it three things it can ask. Everything
// this package needs is in its own signature, which is the property a type
// taking *Node does not have: that one can reach anything the node has, so
// nothing about it says what it actually depends on, and it cannot be built
// anywhere except beside the node.
//
// # What it does
//
// Both arms of a zone. The records an author wrote, and the function they named
// for the names no file could carry. Finding which zone covers a query name is a
// walk up its labels and is shared by both, because two loops is how the arms
// would come to disagree about which zone a name is in.
package zone

import (
	"context"
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/dns"
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

var (
	_ dns.Zones     = (*Zones)(nil)
	_ dns.Resolvers = (*Zones)(nil)
)

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

// covering walks a query name up its labels to the zone that declares it.
//
// # The walk is up the labels, and it stops at the first zone
//
// A query for `mail.shop.example.com` may be answered by a zone declared at
// `shop.example.com` or at `example.com`, and the index maps an exact name to a
// project. So the name is tried, then its parent, then its parent's — most
// specific first, which is the same precedence a resolver would apply between
// two delegations.
//
// IT STOPS AT TWO LABELS. A query for `com` or `.` is not a zone anybody here
// declares, and walking to the root would ask the index about every public
// suffix on every miss.
//
// # What it costs on a miss
//
// One index lookup per label, and a miss is the ordinary case for a server that
// also answers routed names. The lookups are what the index is for and the
// answer is cached by whatever holds the project — but a deep name that matches
// nothing pays for its depth, which is stated rather than hidden.
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
	if at.zone.ResolverName == "" {
		return nil, nil
	}
	if z.guests == nil {
		return nil, nil
	}

	// THE RESOLVER'S OWN SPEC, resolved like any other function — its own access
	// block, its own limits, its own artifact. Nothing about being a resolver
	// changes how it is found.
	resolver, err := z.projects.Resolve(ctx, worker.Target{
		Project:     at.project,
		Environment: at.environment,
		Scope:       Namespace(at.zone.ResolverNamespace),
		Kind:        "function",
		Name:        at.zone.ResolverName,
	})
	if err != nil {
		return nil, fmt.Errorf("zone: finding resolver %q for %s: %w", at.zone.ResolverName, at.zone.Fqdn, err)
	}

	return z.guests.Resolve(ctx, resolver, q)
}

// Namespace splits a declared namespace into the scope chain a target wants.
//
// EXPORTED BECAUSE THE ROOT HAS ITS OWN COPY. node/admission.go splits one the
// same way for a validator's scope; this is the same rule and the two should not
// drift. Moving the other callers onto it is for whoever touches them next.
func Namespace(namespace string) []string {
	namespace = strings.Trim(namespace, "/")
	if namespace == "" {
		return nil
	}
	return strings.Split(namespace, "/")
}
