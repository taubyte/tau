// Package dream runs the discovery service on a peer of an in-process mesh, with a
// real libp2p host and gossipsub topic.
package dream

import (
	"context"
	"io"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/p2p/mesh"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/services/discovery"
)

// Start runs a discovery service on a new peer of m, and answers the peer. The service is
// closed with the mesh, before its peer.
func Start(m *mesh.Mesh) (peer.Node, error) {
	return m.Add(func(node peer.Node) error {
		svc, err := create(m.Context(), node.Peer(), node, m.Name())
		if err != nil {
			return err
		}
		m.OnClose(svc)
		return nil
	})
}

func create(ctx context.Context, h host.Host, node peer.Node, scope string) (io.Closer, error) {
	// The bus is built from the node's own pubsub, scoped to this universe:
	// two universes on one machine must not pool their load figures and route
	// each other's traffic.
	bus, err := discovery.NewBus(node.Messaging(), scope)
	if err != nil {
		return nil, err
	}

	svc, err := discovery.New(h, discovery.Config{},
		discovery.WithBus(bus),
		discovery.WithReporter(reporter(h)),
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

// reporter is what this node says about itself inside a universe: the host's real
// addresses, and zero load, since nothing measures it.
func reporter(h host.Host) discovery.Reporter {
	return discovery.ReporterFunc(func(context.Context) (discovery.Snapshot, error) {
		addrs := make([]string, 0, len(h.Addrs()))
		for _, a := range h.Addrs() {
			addrs = append(addrs, a.String())
		}
		return discovery.Snapshot{Ready: true, Addresses: addrs}, nil
	})
}
