package dns

import (
	"context"
	"errors"
	"strings"

	"github.com/miekg/dns"

	pkgregister "github.com/taubyte/tau/pkg/register"
)

// generated.go answers the cloud's own names: `<label>.<project label>.<cloud>`,
// the hosts a project answers on without declaring a zone of its own.
//
// # Which names, and who answers them
//
// A GENERATED NAME IS SERVED BY A GATEWAY, any gateway — the gateway reads the
// Host header and finds the project itself — so the addresses are those of the
// nodes reporting the gateway service, from the same discovery read every other
// answer here is made of.
//
// BUT ONLY FOR A NAME SOMEBODY PUBLISHED. Answering every name under the cloud
// with every gateway would make the zone a wildcard: any name resolves, a CA is
// happy to validate it, and a typo reaches a gateway that 404s instead of a
// resolver that says the name is not there. The register's host index is what a
// gateway reads to route the request, so it is what decides here too, and a
// name it does not hold is NXDOMAIN — the one statement about existence this
// server can make from what it knows.
//
// # The project label alone is never NXDOMAIN
//
// `<project label>.<cloud>` is the parent of every generated name its project
// publishes, and a resolver minimising its query names (RFC 9156) asks it on the
// way down. NXDOMAIN there means "nothing exists below this" (RFC 8020), which
// would cut off every name under it. The host index is keyed by whole name and
// cannot say what lies below one, so an unpublished name ONE label under the
// cloud is answered NOERROR with no records: true whether or not it has children.

// Hosts is the register's host index: which project answers on a name.
// pkgregister.ErrNoHost is the answer "nobody"; any other error is a register
// that could not be asked.
//
// The node's register source satisfies it, the same read the gateway makes.
type Hosts interface {
	ResolveHost(ctx context.Context, fqdn string) (project, environment string, err error)
}

// Generated is the cloud's zone and what answers on it.
type Generated struct {
	// Zone is the cloud's fqdn, e.g. "tau.example.io" — the gateway's
	// CloudFqdn, the same value, not a second declaration of it.
	Zone string

	// Hosts says which names under Zone are published.
	Hosts Hosts

	// Service is the name the gateway nodes report in discovery.
	Service string
}

// ErrBadGenerated is a generated zone missing one of its three parts.
var ErrBadGenerated = errors.New("dns: a generated zone needs a zone, a host index and the service answering it")

// WithGenerated answers the cloud's generated names. A server without one
// answers them like any other name: by routing, which nothing reports.
func WithGenerated(g Generated) Option {
	return func(o *options) error {
		g.Zone = strings.ToLower(strings.Trim(g.Zone, "."))
		if g.Zone == "" || g.Hosts == nil || g.Service == "" {
			return ErrBadGenerated
		}
		o.generated = &g
		return nil
	}
}

// answerGenerated answers a name under the cloud's zone, or reports false when
// the name is not one (no zone configured, outside it, or the apex itself, which
// is the operator's and carries on to everything else).
func (s *Service) answerGenerated(ctx context.Context, reply *dns.Msg, name string, q dns.Question) bool {
	g := s.opts.generated
	if g == nil {
		return false
	}
	below, ok := strings.CutSuffix(name, "."+g.Zone)
	if !ok || below == "" {
		return false
	}

	_, _, err := g.Hosts.ResolveHost(ctx, name)
	switch {
	case errors.Is(err, pkgregister.ErrNoHost):
		if !strings.Contains(below, ".") {
			return true // the project label: see the file doc
		}
		reply.Rcode = dns.RcodeNameError
		return true
	case err != nil:
		// The register could not be asked, which says nothing about the name.
		reply.Rcode = dns.RcodeServerFailure
		return true
	}

	nodes := s.opts.router.Route(g.Service, s.cfg.MaxAge)
	if len(nodes) == 0 {
		// Published, and no gateway is reporting: unserved, not absent.
		reply.Rcode = dns.RcodeServerFailure
		return true
	}
	switch q.Qtype {
	case dns.TypeA:
		reply.Answer = s.records(nodes, q.Name, false)
	case dns.TypeAAAA:
		reply.Answer = s.records(nodes, q.Name, true)
	}
	return true
}
