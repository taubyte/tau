package stats

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	statsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/stats/v2"
)

type descriptor struct {
	Name string
}

// Descriptor is the role a node runs this as. It serves no RPC: what it knows
// arrives over the bus.
var Descriptor = descriptor{Name: "stats"}

// Protocol is what a node's discovery answers this service under. Nothing listens on
// it, since the view is gossiped rather than asked: a service reads the view of the
// stats beside it, which its node names as itself.
var Protocol protocol.ID = rpc.Protocol(string((&statsv2.Report{}).ProtoReflect().Descriptor().FullName()))
