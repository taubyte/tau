package node

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/multiformats/go-multiaddr"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	odo "github.com/taubyte/odo/cli"
	"github.com/taubyte/p2p/keypair"
	"github.com/taubyte/p2p/peer"
)

func createClientNode(ctx context.Context, config *commonIface.GenericConfig, shape, databasePath string) (peer.Node, error) {
	_ctx, ctxC := context.WithCancel(ctx)
	ma, err := multiaddr.NewMultiaddr(config.P2PAnnounce[0])
	if err != nil {
		ctxC()
		return nil, fmt.Errorf("new multiaddr failed with: %s", err)
	}

	port, err := ma.ValueForProtocol(multiaddr.P_TCP)
	if err != nil {
		ctxC()
		return nil, fmt.Errorf("tcp value from protocol failed with: %s", err)
	}

	_port, err := strconv.Atoi(port)
	if err != nil {
		ctxC()
		return nil, fmt.Errorf("strconv atoi failed with: %s", err)
	}

	clientPort, ok := config.Ports["lite"]
	if !ok {
		ctxC()
		return nil, fmt.Errorf("did not fine lite port in config")
	}

	p2pListen := []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", clientPort)}

	_peer, err := commonIface.ConvertToAddrInfo([]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d/p2p/%s", _port, config.Node.Peer().ID().String())})
	if err != nil {
		ctxC()
		return nil, err
	}

	node, err := peer.NewClientNode(_ctx, databasePath+odo.ClientPrefix, keypair.NewRaw(), config.SwarmKey, p2pListen, nil, true, _peer)
	if err != nil {
		ctxC()
		return nil, fmt.Errorf("creating new client node for shape `%s` failed with: %s", shape, err)
	}

	err = node.WaitForSwarm(10 * time.Second)
	if err != nil {
		ctxC()
		return nil, err
	}

	return node, nil
}
