package auth

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// Protocol is the libp2p protocol id this service listens on, walked off the schema.
// It names the package's listener, not the one service mounted behind it.
var Protocol = rpc.Protocol(authv2connect.IdentityServiceName)

// Ingress states which side of the trust boundary a service is on.
type Ingress int

const (
	IngressNone Ingress = iota
	IngressPublic
)

// descriptor is auth's registry entry: name, protocol id and trust posture.
type descriptor struct {
	Name     string
	Protocol protocol.ID
	Ingress  Ingress
}

// Descriptor states that auth answers other tau nodes only; this package never
// imports pkg/http.
var Descriptor = descriptor{
	Name:     "auth",
	Protocol: Protocol,
	Ingress:  IngressNone,
}
