package database

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/database/v2/databasev2connect"
)

// Protocol is the libp2p protocol id this service listens on, walked off the schema.
var Protocol = rpc.Protocol(databasev2connect.DatabaseServiceName)

// Ingress states which side of the trust boundary a service is on.
type Ingress int

const (
	// IngressNone is p2p-only, and the default so silence is safe.
	IngressNone Ingress = iota
	// IngressPublic binds pkg/http and faces the internet.
	IngressPublic
)

type descriptor struct {
	Name     string
	Protocol protocol.ID
	Ingress  Ingress
}

// Descriptor states that the database service answers other tau nodes only: it holds
// every project's data. Authorization is swarm membership, as for the register and
// secrets.
var Descriptor = descriptor{
	Name:     "database",
	Protocol: Protocol,
	Ingress:  IngressNone,
}
