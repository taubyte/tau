package p2p

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_discoverPeersSize(ctx context.Context, module common.Module,
	max uint32,
	nsTimeout uint32,
	discoverIdPtr uint32,
	sizePtr uint32,
) errno.Error {
	peers, err := f.p2pNode.Discover(ctx, int(max), time.Duration(nsTimeout))
	if err != nil {
		return errno.ErrorP2PDiscoverFailed
	}

	bytesPeers := make([][]byte, len(peers))
	for idx, p := range peers {
		bytesPeers[idx] = peer.ToCid(p.ID).Bytes()
	}

	if err0 := f.WriteUint32Le(module, discoverIdPtr, f.generateDiscovery(bytesPeers)); err0 != 0 {
		return err0
	}

	return f.WriteBytesSliceSize(module, sizePtr, bytesPeers)
}

func (f *Factory) W_discoverPeers(ctx context.Context, module common.Module,
	id,
	peersBuf uint32,
) errno.Error {
	return f.WriteBytesSlice(module, peersBuf, f.getDiscovery(id))
}
