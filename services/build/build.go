// Package build holds the two facts both of build's roles need: the protocol id
// the backend serves and the frontend dials, and the role descriptors. The roles
// sit on either side of a trust boundary: the frontend takes webhooks and the
// job API off the internet and holds no credentials or runtime; the backend owns
// the queue, the records and the run, reachable only over p2p. It imports p2p/rpc
// and the generated connect package only, never pkg/http.
package build

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
)

// Protocol is "/tau/build/v2", derived from the schema. Both service blocks mount
// behind it.
var Protocol = rpc.Protocol(buildv2connect.QueueServiceName)

// QueueDomain names the consistency domain build's queue lives in, fixed by
// build.proto. Exported so the composer knows which domain to open.
const QueueDomain = "build/queue"

// Ingress states which side of the trust boundary a role is on
// (service-conventions.md §5a).
type Ingress int

const (
	// IngressNone is p2p only, and the default so silence is safe.
	IngressNone Ingress = iota
	// IngressPublic faces the internet.
	IngressPublic
)

// Descriptor is one role's registry entry: the facts a node needs before it
// constructs anything.
type Descriptor struct {
	Name     string
	Protocol protocol.ID
	Ingress  Ingress
}

// Frontend's Protocol is empty: the public role hosts no RPC surface, so the swarm
// cannot dial it.
var Frontend = Descriptor{Name: "build-frontend", Protocol: "", Ingress: IngressPublic}

// Backend serves both of build.proto's service blocks behind Protocol, and
// faces no internet: every caller it has is a peer libp2p authenticated.
var Backend = Descriptor{Name: "build-backend", Protocol: Protocol, Ingress: IngressNone}
