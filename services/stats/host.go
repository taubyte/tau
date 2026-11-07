package stats

import (
	"context"
	"slices"

	"github.com/libp2p/go-libp2p/core/host"
	manet "github.com/multiformats/go-multiaddr/net"

	"github.com/taubyte/tau/core/service"
)

// Reports is a running service's part of what its node says about itself: a name it
// answers to beyond its own, the sockets it accepts, what it knows about placement.
type Reports interface {
	Report(*Snapshot)
}

// Start runs stats on an instance: its reports gossiped on the peer, scoped to
// the instance's cloud, saying what the instance runs.
func (s *server) Start(ctx context.Context, h service.Host) error {
	var cfg Config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	bus, err := NewBus(h.Peer().Messaging(), h.Cloud())
	if err != nil {
		return err
	}
	if err := s.assemble(h.Peer().Host(), cfg, withBus(bus), withReporter(instanceReport{h})); err != nil {
		return err
	}
	return s.serve(ctx)
}

// instanceReport is what an instance says about itself: the services it runs, and
// whatever each of them adds. Load is zero and said to be: nothing measures it, and
// zero in a fleet that all reports zero is true.
type instanceReport struct{ h service.Host }

func (r instanceReport) Snapshot(context.Context) (Snapshot, error) {
	snap := Snapshot{Ready: true, Addresses: addresses(r.h.Peer().Host())}
	for _, running := range r.h.Running() {
		if running.Name != "" {
			snap.Serving = append(snap.Serving, running.Name)
		}
		if part, ok := running.Service.(Reports); ok {
			part.Report(&snap)
		}
	}
	slices.Sort(snap.Serving)
	snap.Serving = slices.Compact(snap.Serving)
	return snap, nil
}

// addresses is where clients reach this node: the IPs a DNS answer is made of, not
// the multiaddrs the fleet gossips over. It is the IP half of what the host listens
// on, which behind NAT or a bridge may be unreachable: a claim, like the load figure.
func addresses(h host.Host) []string {
	seen := map[string]bool{}
	var routable, loopback []string
	for _, a := range h.Addrs() {
		ip, err := manet.ToIP(a)
		if err != nil {
			// Not an address with an IP in it: a unix socket, a circuit relay.
			continue
		}
		// 0.0.0.0 AND :: ARE NOT ADDRESSES, they are "every address".
		if ip.IsUnspecified() {
			continue
		}
		s := ip.String()
		if seen[s] {
			continue
		}
		seen[s] = true
		if ip.IsLoopback() {
			loopback = append(loopback, s)
			continue
		}
		routable = append(routable, s)
	}
	// Loopback only when nothing else is available, so a node with a real address never
	// publishes 127.0.0.1, while a loopback-only development cloud still resolves.
	if len(routable) > 0 {
		return routable
	}
	return loopback
}
