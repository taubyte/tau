package node

import (
	"context"
	"fmt"

	libp2p "github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/taubyte/odo/config"
	"github.com/taubyte/odo/utils"
	"github.com/taubyte/p2p/peer"
)

func createP2PNodes(ctx context.Context, databasePath, shape string, conf *config.Protocol) error {
	var err error
	if len(conf.Protocols) < 1 { // For elder nodes
		peerInfo, err := utils.ConvertToAddrInfo(conf.Peers)
		if err != nil {
			return err
		}

		conf.Node, err = peer.NewFull(ctx, databasePath, conf.PrivateKey, conf.SwarmKey, conf.P2PListen, conf.P2PAnnounce, true, peer.BootstrapParams{Enable: true, Peers: peerInfo})
		if err != nil {
			return fmt.Errorf("creating new full node failed with: %s", err)
		}
	} else { // Non elder nodes
		conf.Node, err = config.NewNode(ctx, conf, databasePath)
		if err != nil {
			return fmt.Errorf("creating new node for shape `%s` failed with: %s", shape, err)
		}

		// Create client node
		conf.ClientNode, err = createClientNode(ctx, conf, shape, databasePath)
		if err != nil {
			return fmt.Errorf("creating client node failed with: %s", err)
		}
	}

	return nil
}

func convertToAddrInfo(peers []string) ([]libp2p.AddrInfo, error) {
	addr := make([]libp2p.AddrInfo, 0)
	for _, _addr := range peers {
		addrInfo, err := convertToMultiAddr(_addr)
		if err != nil {
			return nil, fmt.Errorf("converting `%s` to multi addr failed with: %s", _addr, err)
		}

		addr = append(addr, *addrInfo)
	}

	return addr, nil
}

func convertToMultiAddr(addr string) (*libp2p.AddrInfo, error) {
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

func convertBootstrap(peers []string, devMode bool) (peer.BootstrapParams, error) {
	if devMode && len(peers) < 1 {
		return peer.StandAlone(), nil
	}

	if len(peers) > 0 {
		peers, err := convertToAddrInfo(peers)
		if err != nil {
			return peer.BootstrapParams{}, fmt.Errorf("converting peers to libp2p addr info failed with: %s", err)
		}

		return peer.Bootstrap(peers...), nil
	}

	return peer.StandAlone(), nil
}
