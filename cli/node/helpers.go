package node

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/utils"

	odo "github.com/taubyte/tau/cli"
	"github.com/taubyte/tau/core/p2p/keypair"
)

func createLiteNode(ctx context.Context, conf *config.Node, shape, storagePath string) (peer.Node, error) {
	ma, err := multiaddr.NewMultiaddr(conf.P2PAnnounce[0])
	if err != nil {
		return nil, fmt.Errorf("new multiaddr failed with: %s", err)
	}

	port, err := ma.ValueForProtocol(multiaddr.P_TCP)
	if err != nil {
		return nil, fmt.Errorf("tcp value from protocol failed with: %s", err)
	}

	_port, err := strconv.Atoi(port)
	if err != nil {
		return nil, fmt.Errorf("strconv atoi failed with: %s", err)
	}

	clientPort, ok := conf.Ports["lite"]
	if !ok {
		return nil, fmt.Errorf("did not fine lite port in config")
	}

	p2pListen := []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", clientPort)}

	_peer, err := utils.ConvertToAddrInfo([]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d/p2p/%s", _port, conf.Node.Peer().ID().String())})
	if err != nil {
		return nil, err
	}

	node, err := peer.NewClientNode(ctx, storagePath+odo.ClientPrefix, keypair.NewRaw(), conf.SwarmKey, p2pListen, nil, true, _peer)
	if err != nil {
		return nil, fmt.Errorf("creating new client node for shape `%s` failed with: %s", shape, err)
	}

	err = node.WaitForSwarm(10 * time.Second)
	if err != nil {
		return nil, err
	}

	return node, nil
}

func createNodes(ctx context.Context, storagePath, shape string, conf *config.Node) error {
	var err error
	if len(conf.Services) < 1 { // For elder nodes
		peerInfo, err := utils.ConvertToAddrInfo(conf.Peers)
		if err != nil {
			return err
		}

		conf.Node, err = peer.NewFull(ctx, storagePath, conf.PrivateKey, conf.SwarmKey, conf.P2PListen, conf.P2PAnnounce, true, peer.BootstrapParams{Enable: true, Peers: peerInfo})
		if err != nil {
			return fmt.Errorf("creating new full node failed with: %s", err)
		}
	} else {
		// Non elder nodes
		conf.Node, err = config.NewNode(ctx, conf, storagePath)
		if err != nil {
			return fmt.Errorf("creating new node for shape `%s` failed with: %s", shape, err)
		}

		// Create client node
		conf.ClientNode, err = createLiteNode(ctx, conf, shape, storagePath)
		if err != nil {
			return fmt.Errorf("creating client node failed with: %s", err)
		}
	}

	return nil
}
