// Package dream runs the stats service on a peer of an in-process mesh.
//
// It is the first v2 service to run under dream, and it is what makes "parity"
// a thing that can be tested rather than asserted: the service that runs here is
// the same code, built the same way, with a real libp2p host and a real
// gossipsub topic under it.
package dream

import (
	"context"
	"io"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/p2p/mesh"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/services/stats"
)

// Start runs a stats service on a new peer of m, and answers the peer. The service is
// closed with the mesh, before its peer.
func Start(m *mesh.Mesh) (peer.Node, error) {
	node, err := m.Add()
	if err != nil {
		return nil, err
	}
	svc, err := create(m.Context(), node.Peer(), node, m.Name())
	if err != nil {
		return nil, err
	}
	m.OnClose(svc)
	return node, nil
}

func create(ctx context.Context, h host.Host, node peer.Node, scope string) (io.Closer, error) {
	// The bus is built from the node's own pubsub, scoped to this universe:
	// two universes on one machine must not pool their load figures and route
	// each other's traffic.
	bus, err := stats.NewBus(node.Messaging(), scope)
	if err != nil {
		return nil, err
	}

	svc, err := stats.New(h, stats.Config{},
		stats.WithBus(bus),
		stats.WithReporter(reporter(h)),
	)
	if err != nil {
		return nil, err
	}
	if err := svc.Start(ctx); err != nil {
		svc.Close()
		return nil, err
	}
	return svc, nil
}

// reporter is what this node says about itself inside a universe.
//
// IT REPORTS A REAL ADDRESS AND NO LOAD IT HAS NOT MEASURED. A dream node is
// reachable at the host's own addresses, so those are what it advertises; load
// stays zero because nothing here measures it yet, and a made-up number would
// steer routing on fiction. Zero is honest: it says "as available as anything
// else", which inside a universe is true.
func reporter(h host.Host) stats.Reporter {
	return stats.ReporterFunc(func(context.Context) (stats.Snapshot, error) {
		addrs := make([]string, 0, len(h.Addrs()))
		for _, a := range h.Addrs() {
			addrs = append(addrs, a.String())
		}
		return stats.Snapshot{Ready: true, Addresses: addrs}, nil
	})
}
