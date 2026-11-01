package primitives

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v2/primitivesv2connect"
)

// Protocol is the libp2p protocol id this service listens on, walked off the schema.
var Protocol = rpc.Protocol(primitivesv2connect.PrimitivesServiceName)

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

// Descriptor states that the primitives service answers other tau nodes only: a
// public ingress would let a stranger claim and acknowledge another project's jobs.
// Authorization is swarm membership.
var Descriptor = descriptor{
	Name:     "primitives",
	Protocol: Protocol,
	Ingress:  IngressNone,
}
