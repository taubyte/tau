package p2p

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/services/common"
)

func (s *Service) Discover(ctx context.Context, max int, timeout time.Duration) ([]peer.AddrInfo, error) {
	_ctx, _ctxC := context.WithTimeout(ctx, timeout)
	defer _ctxC()

	peers, err := s.Node().Discovery().FindPeers(
		_ctx,
		common.SubstrateP2PProtocol,
		discovery.Limit(max+1), // Limit to max+1 because we are discovering ourselves
	)
	if err != nil {
		return nil, fmt.Errorf("finding peers failed with: %w", err)
	}

	addrs := make([]peer.AddrInfo, 0, max)
	for p := range peers {
		if p.ID != s.Node().ID() {
			addrs = append(addrs, p)
		}
	}

	return addrs, nil
}
