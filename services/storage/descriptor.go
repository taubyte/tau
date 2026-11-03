package storage

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2/storagev2connect"
)

var Protocol = rpc.Protocol(storagev2connect.StorageServiceName)

type descriptor struct {
	Name     string
	Protocol protocol.ID
}

// Descriptor is the role a node runs this as. p2p only: no peer outside the swarm
// reaches it, and any peer inside may read and write any project's bytes, as for
// the register, secrets and database.
var Descriptor = descriptor{
	Name:     "storage",
	Protocol: Protocol,
}
