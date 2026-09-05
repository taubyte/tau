package secrets

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v1/secretsv1connect"
)

// Protocol is the libp2p protocol id this service listens on: "/" plus the
// proto package with dots as slashes, walked off the schema rather than typed
// here.
var Protocol = rpc.Protocol(secretsv1connect.SecretsServiceName)

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

// Descriptor states that the secrets service answers other tau nodes only.
//
// IT MUST STAY THAT WAY, and of everything in v2 this is the one where the
// reason needs no elaboration: Get returns the value. There is no
// authorization on any call, so a public ingress would put every secret every
// project holds on the internet.
var Descriptor = descriptor{
	Name:     "secrets",
	Protocol: Protocol,
	Ingress:  IngressNone,
}
