package utils

import (
	"fmt"

	libp2p "github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/p2p/peer"
)

func ConvertBootstrap(peers []string, devMode bool) (peer.BootstrapParams, error) {
	if devMode && len(peers) < 1 {
		return peer.StandAlone(), nil
	}

	if len(peers) > 0 {
		peers, err := ConvertToAddrInfo(peers)
		if err != nil {
			return peer.BootstrapParams{}, fmt.Errorf("converting peers to libp2p addr info failed with: %s", err)
		}

		return peer.Bootstrap(peers...), nil
	}

	return peer.StandAlone(), nil
}

func ConvertToAddrInfo(peers []string) ([]libp2p.AddrInfo, error) {
	addr := make([]libp2p.AddrInfo, 0)
	for _, _addr := range peers {
		addrInfo, err := ConvertToMultiAddr(_addr)
		if err != nil {
			return nil, fmt.Errorf("converting `%s` to multi addr failed with: %s", _addr, err)
		}

		addr = append(addr, *addrInfo)
	}

	return addr, nil
}

func ConvertToMultiAddr(addr string) (*libp2p.AddrInfo, error) {
	_multiaddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return nil, fmt.Errorf("converting `%s` to a multi address failed with: %s", addr, err)
	}

	addrInfo, err := libp2p.AddrInfoFromP2pAddr(_multiaddr)
	if err != nil {
		return nil, fmt.Errorf("getting addr from p2p addr failed with: %s", err)
	}

	return addrInfo, nil

}
