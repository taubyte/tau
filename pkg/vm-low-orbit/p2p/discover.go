package p2p

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) discoverPeersSize(ctx context.Context, module common.Module,
	max uint32,
	nsTimeout uint32,
	discoverIdPtr uint32,
	sizePtr uint32,
) uint32 {
	peers, err := f.p2pNode.Discover(ctx, int(max), time.Duration(nsTimeout))
	if err != nil {
		return uint32(errno.ErrorP2PDiscoverFailed)
	}

	bytesPeers := make([][]byte, len(peers))
	for idx, p := range peers {
		bytesPeers[idx] = peer.ToCid(p.ID).Bytes()
	}

	if err0 := f.WriteUint32Le(module, discoverIdPtr, f.generateDiscovery(bytesPeers)); err0 != 0 {
		return uint32(err0)
	}

	return uint32(f.WriteBytesSliceSize(module, sizePtr, bytesPeers))
}

func (f *Factory) discoverPeers(ctx context.Context, module common.Module,
	id,
	peersBuf uint32,
) uint32 {
	return uint32(f.WriteBytesSlice(module, peersBuf, f.getDiscovery(id)))
}
