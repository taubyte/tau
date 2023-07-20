package node

import (
	"context"
	"fmt"

	oldp2p "bitbucket.org/taubyte/p2p/peer"
	"github.com/taubyte/odo/config"
	"github.com/taubyte/odo/utils"
)

func createP2PNodes(ctx context.Context, databasePath, shape string, conf *config.Protocol) error {
	var err error
	if len(conf.Protocols) < 1 { // For elder nodes
		peerInfo, err := utils.ConvertToAddrInfo(conf.Peers)
		if err != nil {
			return err
		}

		conf.Node, err = oldp2p.NewFull(ctx, databasePath, conf.PrivateKey, conf.SwarmKey, conf.P2PListen, conf.P2PAnnounce, true, oldp2p.BootstrapParams{Enable: true, Peers: peerInfo})
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
