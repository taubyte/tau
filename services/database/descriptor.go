package database

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/database/v2/databasev2connect"
)

// Protocol is the libp2p protocol id this service listens on: "/" plus the
// proto package with dots as slashes, walked off the schema rather than typed
// here.
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

// Descriptor states that the database service answers other tau nodes only.
//
// IT HOLDS EVERY PROJECT'S DATA, which makes the reason short: a public ingress
// would put every row every project has written on the internet. Authorization
// is the swarm's membership, exactly as it is for the register and the secrets
// service — see those packages for why that is the OSS trust model rather than
// an omission.
var Descriptor = descriptor{
	Name:     "database",
	Protocol: Protocol,
	Ingress:  IngressNone,
}
