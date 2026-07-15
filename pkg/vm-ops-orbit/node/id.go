package node

import (
	"context"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/vm"
)

func (f *Factory) getNodeId(ctx context.Context, module vm.Module, cidPtr uint32) uint32 {
	return uint32(f.WriteCid(module, cidPtr, peer.ToCid(f.node.Node().ID())))
}
