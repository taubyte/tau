package common

import (
	"fmt"
	"os"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/pkg/config"
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

func StartSeerBeacon(cfg config.Config, sc seer.Client, serviceType seer.ServiceType, ops ...seerBeaconOption) error {
	seerConfig := &seerBeaconConfig{
		serviceMeta: make(map[string]string, 0),
	}
	for _, op := range ops {
		op(seerConfig)
	}

	// Create a Geo Beacon if location was provided
	if cfg.Location() != nil {
		sc.Geo().Beacon(*cfg.Location()).Start()
	}

	announce := cfg.P2PAnnounce()
	if len(announce) == 0 {
		return fmt.Errorf("p2p announce is empty")
	}
	ma, err := multiaddr.NewMultiaddr(announce[0])
	if err != nil {
		return err
	}

	// Report Ip address as well
	addr, err := ma.ValueForProtocol(multiaddr.P_IP4)
	if err != nil {
		return err
	}
	seerConfig.serviceMeta["IP"] = addr
	if cfg.Cluster() != "" {
		seerConfig.serviceMeta["cluster"] = cfg.Cluster()
	} else {
		seerConfig.serviceMeta["cluster"] = "main"
	}

	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed finding hostname on `%s` with: %s", serviceType, err)
	}

	var nodeId, clientNodeId string
	var signature []byte

	if cfg.ClientNode() != nil && cfg.Node() != nil {
		nodeId = cfg.Node().ID().String()
		clientNodeId = cfg.ClientNode().ID().String()

		// Get signature from private key
		privKey, err := crypto.UnmarshalPrivateKey(cfg.PrivateKey())
		if err != nil {
			return fmt.Errorf("unmarshal private key failed with: %s", err)
		}

		signature, err = privKey.Sign([]byte(cfg.Node().ID().String() + cfg.ClientNode().ID().String()))
		if err != nil {
			return fmt.Errorf("signing private key failed with: %s", err)
		}
	}

	// Start Usage Beacon
	sc.Usage().AddService(serviceType, seerConfig.serviceMeta)
	sc.Usage().Beacon(hostname, nodeId, clientNodeId, signature).Start()

	return nil
}
