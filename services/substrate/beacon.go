package substrate

import (
	"fmt"
	"os"

	crypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/multiformats/go-multiaddr"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	"github.com/taubyte/tau/config"
	seerIface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/services/substrate/components/p2p/common"
)

type serviceBeacon struct {
	*Service
	config     *config.Node
	seerClient seerIface.Client
}

// TODO: REMOVE
func (srv *Service) startBeacon(config *config.Node) (beacon *serviceBeacon, err error) {
	beacon = &serviceBeacon{Service: srv, config: config}

	// For Dev
	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	beacon.seerClient, err = seerClient.New(srv.ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("creating seer client failed with %s", err)
	}

	if beacon.config.Location != nil {
		beacon.seerClient.Geo().Beacon(*beacon.config.Location)
	}

	return
}

func (beacon *serviceBeacon) hostname() (err error) {
	ma, err := multiaddr.NewMultiaddr(beacon.config.P2PAnnounce[0])
	if err != nil {
		common.Logger.Error(err)
		return err
	}

	addr, err := ma.ValueForProtocol(multiaddr.P_IP4)
	if err != nil {
		common.Logger.Error(err)
		return err
	}

	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed finding hostname with %v", err)
	}

	var nodeId, clientNodeId string
	var signature []byte

	// ---- FOR ODO
	if beacon.config.ClientNode != nil && beacon.config.Node != nil {
		nodeId = beacon.config.Node.ID().String()
		clientNodeId = beacon.config.ClientNode.ID().String()

		// Get signature from private key
		privKey, err := crypto.UnmarshalPrivateKey(beacon.config.PrivateKey)
		if err != nil {
			return fmt.Errorf("unmarshal private key failed with: %s", err)
		}

		signature, err = privKey.Sign([]byte(beacon.config.Node.ID().String() + beacon.config.ClientNode.ID().String()))
		if err != nil {
			return fmt.Errorf("signing private key failed with: %s", err)
		}
	}

	// Start usage beacon
	beacon.seerClient.Usage().AddService(seerIface.ServiceTypeSubstrate, map[string]string{"IP": addr})
	beacon.seerClient.Usage().Beacon(hostname, nodeId, clientNodeId, signature).Start()

	return nil
}
