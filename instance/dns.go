package instance

import (
	"net"

	"github.com/taubyte/tau/services/dns"
)

// DNS runs from the catalog (services/dns) and answers from what this node gives it:
// the stats view, the worker and its component tier, the names below, and the DNS-01
// values in certificates (cm.go).

// DNSAddrs is where this node's dns server bound, or nothing. It is how a caller that
// asked for port 0 finds out, which is what a test does.
func (n *Instance) DNSAddrs() []net.Addr {
	for _, r := range n.runningNow() {
		if d, ok := r.Service.(*dns.Service); ok {
			return d.Addrs()
		}
	}
	return nil
}

// catalog is every service name a node of this composition can report, for dns to
// answer `<service>.<zone>` with.
type catalog struct{ n *Instance }

func (c catalog) ServiceNames() []string { return c.n.knownServices() }

// knownServices is every name the catalog and this package's tests add.
func (n *Instance) knownServices() []string { return n.opts.serviceNames() }
