package worker

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/worker/v2/workerv2connect"
)

// Protocol is the libp2p protocol id this service listens on, walked off the
// schema rather than typed here.
var Protocol = rpc.Protocol(workerv2connect.WorkerServiceName)

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

// Descriptor states that a worker answers other tau nodes only.
//
// A REQUEST FROM THE INTERNET ARRIVES THROUGH A GATEWAY, which is what parses
// it, matches a route and decides what may be reached. A worker that also
// listened on the internet would be a second door into every workload with no
// route, no method matching and no policy in front of it.
var Descriptor = descriptor{
	Name:     "worker",
	Protocol: Protocol,
	Ingress:  IngressNone,
}
