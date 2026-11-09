package dns

import (
	"context"
	"errors"
	"strings"

	"github.com/miekg/dns"

	pkgregister "github.com/taubyte/tau/pkg/register"
)

// The cloud's own names: `<label>.<project label>.<cloud>`, the hosts a project
// answers on without a zone of its own. Any gateway serves one, so the addresses are
// the nodes reporting the gateway service, unless nodes report serving the name
// itself, as a proxy does each host it serves.
//
// Only a published name resolves; answering every name would make the zone a
// wildcard a CA would validate. The register's host index decides, as it does for
// the gateway, and a name it does not hold is NXDOMAIN.
//
// `<project label>.<cloud>` alone is never NXDOMAIN: a resolver minimising query
// names (RFC 9156) asks it on the way down, and NXDOMAIN would cut off every name
// below (RFC 8020). An unpublished name one label under the cloud is NOERROR with no
// records.

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
	// Zone is the cloud's fqdn, e.g. "tau.example.io": the node's cloudfqdn.
	Zone string

	// Hosts says which names under Zone are published.
	Hosts Hosts

	// Service is the name the gateway nodes report in discovery.
	Service string
}

// ErrBadGenerated is a generated zone missing one of its three parts.
var ErrBadGenerated = errors.New("dns: a generated zone needs a zone, a host index and the service answering it")

// withGenerated answers the cloud's generated names. A server without one
// answers them like any other name: by routing, which nothing reports.
func withGenerated(g Generated) option {
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

	// A NAME A NODE REPORTS SERVING BY NAME is answered by those nodes: a proxy
	// reports each host it serves, and a gateway serves them all and reports none.
	nodes := s.opts.router.Route(name, s.cfg.MaxAge)
	if len(nodes) == 0 {
		nodes = s.opts.router.Route(g.Service, s.cfg.MaxAge)
	}
	if len(nodes) == 0 {
		// Published, and nothing serving it is reporting: unserved, not absent.
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
