package register

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/register/v2/registerv2connect"
)

// Protocol is the libp2p protocol id this service listens on: "/" plus the
// proto package with dots as slashes, walked off the schema rather than typed
// here. The "tau." prefix in that package is what keeps this off v1's
// "/tns/v1" (services/common/names.go) for the whole span in which both run in
// one swarm.
var Protocol = rpc.Protocol(registerv2connect.RegisterServiceName)

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

// Descriptor states that the register answers other tau nodes only.
//
// IT MUST STAY THAT WAY. Every runtime value a project declares is here, and
// Publish moves what an environment runs. A public ingress would put "replace
// the code this project runs" on the internet behind whatever authorization
// this service does not have.
var Descriptor = descriptor{
	Name:     "register",
	Protocol: Protocol,
	Ingress:  IngressNone,
}
