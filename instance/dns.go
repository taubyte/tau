package instance

import (
	"errors"
	"fmt"
	"net"

	"github.com/taubyte/tau/instance/zone"
	"github.com/taubyte/tau/services/dns"
)

// Answers queries from this node's discovery view: the last link, without which
// proxies' ports published through discovery are unreadable. Only the port is
// configurable, since 53 is dialled by convention. The default :53 needs privilege,
// and a failed bind is reported rather than swallowed.

// DefaultDNSListen is every address this machine has, on the port a resolver
// dials by convention.
const DefaultDNSListen = ":53"

// ErrNoDiscoveryForDNS is a dns node with nothing to answer from.
var ErrNoDiscoveryForDNS = errors.New("node: dns answers from the discovery view, and this node runs none")

func (n *Instance) openDNS() error {
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

	// THE CLOUD'S GENERATED NAMES, when the node knows the cloud's zone: the
	// register's host index says which are published, and the nodes reporting
	// the gateway service answer them.
	if zone := n.cfg.Gateway.CloudFqdn; zone != "" {
		options = append(options, dns.WithGenerated(dns.Generated{
			Zone: zone, Hosts: n.registerSource(), Service: Gateway,
		}))
		// AND ITS SERVICES BY NAME, `<service>.<cloud>`, answered by the nodes
		// reporting each one. The names are every service a node can run here,
		// added ones included, so a name outside them is refused as absent.
		options = append(options, dns.WithServices(dns.Services{Zone: zone, Names: n.knownServices()}))
	}

	// AND DNS-01, when the cloud issues: this node's copy of the
	// challenge store holds the TXT values of the orders being proved.
	challenges, err := n.challengeReader()
	if err != nil {
		return err
	}
	if challenges != nil {
		options = append(options, dns.WithChallenges(n.cfg.Gateway.CloudFqdn, challenges))
	}

	svc, err := dns.New(n.cfg.DNS, options...)
	if err != nil {
		return err
	}

	n.dns = svc
	n.closers = append(n.closers, svc)
	return nil
}

// listenDNS binds the server, separate from openDNS so building and binding fail
// separately.
func (n *Instance) listenDNS() error {
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
func (n *Instance) DNSAddrs() []net.Addr {
	if n.dns == nil {
		return nil
	}
	return n.dns.Addrs()
}

// knownServices is every service name a node of this composition can report:
// the built-in ones, the public build role All leaves out, and the ones added
// with WithService.
func (n *Instance) knownServices() []string {
	return append(builtIn(), n.opts.serviceNames()...)
}
