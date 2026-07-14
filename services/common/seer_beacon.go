package common

import (
	"fmt"
	"os"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/pkg/config"
)

// StartSeerBeacon announces the given service types from this node through a
// single seer client and one usage/geo beacon goroutine set. A node running
// several services (a shape) beacons once for all of them, instead of opening a
// client and beacon per service. Callers build the client (see
// clients/p2p/seer.StartNodeBeacon) so this package need not depend on it.
func StartSeerBeacon(cfg config.Config, sc seer.Client, serviceTypes []seer.ServiceType) error {
	if len(serviceTypes) == 0 {
		return nil
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

	cluster := cfg.Cluster()
	if cluster == "" {
		cluster = "main"
	}

	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed finding hostname with: %s", err)
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

	// Announce every service type this node runs under one usage beacon.
	for _, serviceType := range serviceTypes {
		sc.Usage().AddService(serviceType, map[string]string{"IP": addr, "cluster": cluster})
	}
	sc.Usage().Beacon(hostname, nodeId, clientNodeId, signature).Start()

	return nil
}
