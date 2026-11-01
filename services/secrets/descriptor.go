package secrets

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2/secretsv2connect"
)

// Protocol is the libp2p protocol id this service listens on, walked off the schema.
var Protocol = rpc.Protocol(secretsv2connect.SecretsServiceName)

type Ingress int

const (
	IngressNone Ingress = iota
	IngressPublic
)

type descriptor struct {
	Name     string
	Protocol protocol.ID
	Ingress  Ingress
}

// Descriptor states that the secrets service answers other tau nodes only. Get returns
// the value and no call is authorized, so a public ingress would expose every secret.
var Descriptor = descriptor{
	Name:     "secrets",
	Protocol: Protocol,
	Ingress:  IngressNone,
}
