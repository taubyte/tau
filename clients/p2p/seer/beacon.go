package seer

import (
	"context"
	"fmt"

	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/services/common"
)

// StartNodeBeacon opens a single seer client on node and beacons every given
// service type through it (one usage + geo beacon set), so a node running a
// shape of services announces them all with one client and goroutine set
// instead of one per service. It is a no-op when no types are given or node is
// nil. node is passed explicitly because in Dream the node lives on the service,
// not on cfg.
//
// This lives here rather than in services/common because building the client
// needs this package, which services/common cannot import (import cycle).
func StartNodeBeacon(ctx context.Context, cfg config.Config, node peer.Node, serviceTypes ...iface.ServiceType) error {
	if node == nil || len(serviceTypes) == 0 {
		return nil
	}

	sc, err := New(ctx, node, cfg.SensorsRegistry())
	if err != nil {
		return fmt.Errorf("creating seer client failed with: %w", err)
	}

	return common.StartSeerBeacon(cfg, sc, serviceTypes)
}
