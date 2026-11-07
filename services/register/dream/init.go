// Package dream runs the register on a peer of an in-process mesh, over a real
// replicated kvdb, so a cloud can show a project published on one node resolving
// on another.
package dream

import (
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/mesh"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/services/register"
)

// Start runs a register on a new peer of m, and answers the peer. The service is
// closed with the mesh, before its peer.
func Start(m *mesh.Mesh) (peer.Node, error) {
	return m.Add(func(node peer.Node) error {
		svc := register.New()
		m.OnClose(svc)
		return service.On(node, m.Name()).Start(m.Context(), register.Descriptor.Name, svc)
	})
}
