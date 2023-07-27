package p2p

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/protocols/substrate/components/p2p/common"
)

func (s *Service) Discover(ctx context.Context, max int, timeout time.Duration) ([]peer.AddrInfo, error) {
	peers, err := s.Node().Discovery().FindPeers(
		ctx,
		common.Protocol,
		// Limit to max+1 because we are discovering ourselves
		discovery.Limit(max+1),
		discovery.TTL(timeout),
	)
	if err != nil {
		return nil, err
	}

	addrs := make([]peer.AddrInfo, 0, max)
	for p := range peers {
		if p.ID != s.Node().ID() {
			addrs = append(addrs, p)
		}
	}

	return addrs, nil
}
