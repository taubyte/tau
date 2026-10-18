package node

import (
	"errors"
	"fmt"
	"net"

	"github.com/taubyte/tau/node/zone"
	"github.com/taubyte/tau/services/dns"
)

// dns.go answers queries from what this node's discovery view holds.
//
// # It is the last link, and without it the rest publishes into nothing
//
// A proxy binds a port the kernel chose and reports it; discovery carries that to
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

// ErrNoDiscoveryForDNS is a dns node with nothing to answer from.
var ErrNoDiscoveryForDNS = errors.New("node: dns answers from the discovery view, and this node runs none")

func (n *Node) openDNS() error {
	if !n.cfg.runs(DNS) {
		return nil
	}
	if n.discovery == nil {
		return ErrNoDiscoveryForDNS
	}

	options := []dns.Option{dns.WithRouter(n.discovery)}

	// AUTHORED RECORDS NEED A WORKER, because the records are in the compiled
	// project and the worker is what holds one. A dns node without one answers
	// what it can route and nothing an author wrote — which is a real loss and
	// is not a reason to refuse to answer at all.
	if n.worker != nil {
		// BOTH ARMS FROM ONE ADAPTER: it holds the label walk that finds which
		// zone covers a name, and the resolver is named by that zone. The
		// component tier is passed as-is — nil on a node that runs none, and a
		// zone naming a resolver it cannot run then answers nothing rather than
		// answering that the name is gone.
		z, err := zone.New(n.registerSource(), n.worker, n.wasmTier())
		if err != nil {
			return err
		}
		options = append(options, dns.WithZones(z), dns.WithResolvers(z))
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
