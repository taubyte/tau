package node

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/taubyte/tau/services/dns"
)

// dns.go answers queries from what this node's stats view holds.
//
// # It is the last link, and without it the rest publishes into nothing
//
// A proxy binds a port the kernel chose and reports it; stats carries that to
// every node; and a client learns it by looking up an SRV record. Nothing in
// that chain is reachable until something binds a socket and answers — so a
// fleet with no dns node has a routing table nobody can read.
//
// # Where it listens is a port, and only a port
//
// Same rule as the proxy's: the addresses are the machine's and nothing here
// names one. What an operator does choose is the PORT, because 53 is the one
// number in this system that is not allocated — a resolver dials it by
// convention and there is no record that could tell it otherwise.
//
// THE DEFAULT IS :53 AND BINDING IT NEEDS PRIVILEGE. Below 1024 a process needs
// root or CAP_NET_BIND_SERVICE, which a development node has neither of, so the
// failure is reported rather than swallowed: a node told to serve dns and
// silently serving nothing is a fleet whose names resolve until the one node
// that could answer them restarts.

// DefaultDNSListen is every address this machine has, on the port a resolver
// dials by convention.
const DefaultDNSListen = ":53"

// ErrNoStatsForDNS is a dns node with nothing to answer from.
var ErrNoStatsForDNS = errors.New("node: dns answers from the stats view, and this node runs none")

func (n *Node) openDNS() error {
	if !n.cfg.runs(DNS) {
		return nil
	}
	if n.stats == nil {
		return ErrNoStatsForDNS
	}

	options := []dns.Option{dns.WithRouter(n.stats)}

	// AUTHORED RECORDS NEED A WORKER, because the records are in the compiled
	// project and the worker is what holds one. A dns node without one answers
	// what it can route and nothing an author wrote — which is a real loss and
	// is not a reason to refuse to answer at all.
	if n.worker != nil {
		options = append(options, dns.WithZones(&zones{node: n}))
	}

	svc, err := dns.New(n.cfg.DNS, options...)
	if err != nil {
		return err
	}

	n.dns = svc
	n.closers = append(n.closers, svc)
	return nil
}

// listenDNS binds the server. It is separate from openDNS for the reason the
// gateway's handler is separate from its construction: building a service and
// taking a socket are different failures, and a composition that did both at
// once could not be tested without one.
func (n *Node) listenDNS() error {
	if n.dns == nil {
		return nil
	}

	on := n.cfg.DNSListen
	if on == "" {
		on = DefaultDNSListen
	}

	if err := n.dns.Listen(on); err != nil {
		return fmt.Errorf("node: serving dns on %s: %w", on, err)
	}
	return nil
}

// DNSAddrs is where this node's dns server bound, or nothing. It is how a
// caller that asked for port 0 finds out, which is what a test does.
func (n *Node) DNSAddrs() []net.Addr {
	if n.dns == nil {
		return nil
	}
	return n.dns.Addrs()
}

// zones resolves a query name to the records a project declares for it.
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
// declares, and walking to the root would ask the register about every public
// suffix on every miss.
//
// # What it costs on a miss
//
// One index lookup per label, and a miss is the ordinary case for a server that
// also answers routed names. The lookups are what the register is for and the
// answer is cached by whatever holds the project — but a deep name that matches
// nothing pays for its depth, which is stated rather than hidden.
type zones struct{ node *Node }

var _ dns.Zones = (*zones)(nil)

func (z *zones) Records(ctx context.Context, name string) (string, []string, error) {
	labels := strings.Split(strings.ToLower(strings.TrimSuffix(name, ".")), ".")

	source := z.node.registerSource()
	for i := 0; i+1 < len(labels); i++ {
		apex := strings.Join(labels[i:], ".")

		projectName, environment, err := source.ResolveHost(ctx, apex)
		if err != nil {
			continue
		}

		proj, _, err := z.node.worker.At(ctx, projectName, environment)
		if err != nil {
			// The index says this project owns the apex and its compiled form
			// is not readable here. The next label up is not the answer — this
			// one is, and it is unavailable — so the walk stops.
			return "", nil, fmt.Errorf("node: reading %s/%s for %s: %w", projectName, environment, apex, err)
		}

		for _, declared := range proj.Zones() {
			if declared.Fqdn == apex {
				return declared.Fqdn, declared.Records, nil
			}
		}

		// THE INDEX MATCHED AND THE PROJECT DECLARES NO SUCH ZONE. That is a
		// gateway host at this name rather than a zone apex, so the walk
		// carries on: a project may serve `shop.example.com` from a gateway
		// and still declare `example.com` as a zone.
	}
	return "", nil, nil
}
