package common

import (
	"fmt"
	"os"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/core/services/seer"
)

type seerBeaconConfig struct {
	serviceMeta map[string]string
}

type seerBeaconOption func(cnf *seerBeaconConfig)

func SeerBeaconOptionMeta(meta map[string]string) seerBeaconOption {
	return func(cnf *seerBeaconConfig) {
		cnf.serviceMeta = meta
	}
}

func StartSeerBeacon(config *config.Node, sc seer.Client, serviceType seer.ServiceType, ops ...seerBeaconOption) error {
	seerConfig := &seerBeaconConfig{
		serviceMeta: make(map[string]string, 0),
	}
	for _, op := range ops {
		op(seerConfig)
	}

	// Create a Geo Beacon if location was provided
	if config.Location != nil {
		sc.Geo().Beacon(*config.Location).Start()
	}

	ma, err := multiaddr.NewMultiaddr(config.P2PAnnounce[0])
	if err != nil {
		return err
	}

	// Report Ip address as well
	addr, err := ma.ValueForProtocol(multiaddr.P_IP4)
	if err != nil {
		return err
	}
	seerConfig.serviceMeta["IP"] = addr

	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed finding hostname on `%s` with: %s", serviceType, err)
	}

	var nodeId, clientNodeId string
	var signature []byte

	if config.ClientNode != nil && config.Node != nil {
		nodeId = config.Node.ID().String()
		clientNodeId = config.ClientNode.ID().String()

		// Get signature from private key
		privKey, err := crypto.UnmarshalPrivateKey(config.PrivateKey)
		if err != nil {
			return fmt.Errorf("unmarshal private key failed with: %s", err)
		}

		signature, err = privKey.Sign([]byte(config.Node.ID().String() + config.ClientNode.ID().String()))
		if err != nil {
			return fmt.Errorf("signing private key failed with: %s", err)
		}
	}

	// Start Usage Beacon
	sc.Usage().AddService(serviceType, seerConfig.serviceMeta)
	sc.Usage().Beacon(hostname, nodeId, clientNodeId, signature).Start()

	return nil
}
