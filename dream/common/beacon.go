package common

import (
	"context"

	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	seerIface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/p2p/peer"
	tauConfig "github.com/taubyte/tau/pkg/config"
)

// StartBeacon makes a Dream-hosted service announce itself to seer, the same way
// a production node does in cli/node. Each Dream service is its own node, so it
// beacons only its own type. Kept here so the per-service dream/init.go files
// stay one-liners. node is the service's own node (Dream never sets cfg.Node()).
func StartBeacon(ctx context.Context, cfg tauConfig.Config, node peer.Node, name string) error {
	return seerClient.StartNodeBeacon(ctx, cfg, node, seerIface.ServiceType(name))
}
