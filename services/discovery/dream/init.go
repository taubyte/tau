// Package dream runs the discovery service on a peer of an in-process mesh, with a
// real libp2p host and gossipsub topic.
package dream

import (
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/mesh"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/services/discovery"
)

// Start runs a discovery service on a new peer of m, and answers the peer. The service is
// closed with the mesh, before its peer.
func Start(m *mesh.Mesh) (peer.Node, error) {
	return m.Add(func(node peer.Node) error {
		svc := discovery.New()
		m.OnClose(svc)
		// The gossip is scoped to this universe: two universes on one machine must
		// not pool their load figures and route each other's traffic.
		return svc.Start(m.Context(), &meshHost{Node: node, cloud: m.Name(), discovery: svc})
	})
}

// meshHost is the instance a mesh peer is: its peer, the one service it runs, in
// the universe's cloud.
type meshHost struct {
	peer.Node
	cloud     string
	discovery discovery.Service
}

func (h *meshHost) Peer() peer.Node { return h.Node }
func (h *meshHost) Running() []service.Running {
	return []service.Running{{Name: discovery.Descriptor.Name, Service: h.discovery}}
}
func (h *meshHost) Config(any) error { return nil }
func (h *meshHost) Root() string     { return "" }
func (h *meshHost) Cloud() string    { return h.cloud }
