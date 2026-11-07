// Package dream runs the stats service on a peer of an in-process mesh, with a
// real libp2p host and gossipsub topic.
package dream

import (
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/mesh"
	"github.com/taubyte/tau/p2p/peer"
	statsService "github.com/taubyte/tau/services/stats"
)

// Start runs a stats service on a new peer of m, and answers the peer. The service is
// closed with the mesh, before its peer.
func Start(m *mesh.Mesh) (peer.Node, error) {
	return m.Add(func(node peer.Node) error {
		svc := statsService.New()
		m.OnClose(svc)
		// The gossip is scoped to this universe: two universes on one machine must
		// not pool their load figures and route each other's traffic.
		return service.On(node, m.Name()).Start(m.Context(), statsService.Descriptor.Name, svc)
	})
}
