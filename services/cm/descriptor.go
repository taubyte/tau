package cm

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2/cmv2connect"
)

// Name is the service's name, in a node's service list and in discovery.
const Name = "cm"

// Protocol is the libp2p protocol the service answers on.
var Protocol protocol.ID = rpc.Protocol(cmv2connect.CertificateServiceName)

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

// Descriptor names the service the way every v2 service names itself.
var Descriptor = descriptor{Name: Name, Protocol: Protocol, Ingress: IngressNone}
