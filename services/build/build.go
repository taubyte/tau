// Package build holds what both of build's roles need: the protocol id the backend
// serves and the frontend dials, the role descriptors, and how a run's log becomes
// the events a follower reads (lines.go). The roles
// sit on either side of a trust boundary: the frontend takes webhooks and the
// job API off the internet and holds no credentials or runtime; the backend owns
// the queue, the records and the run, reachable only over p2p. It imports p2p/rpc
// and the generated code only, never pkg/http.
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

// Ingress states which side of the trust boundary a role is on.
type Ingress int

const (
	// IngressNone is p2p only, and the default so silence is safe.
	IngressNone Ingress = iota
	// IngressPublic faces the internet.
	IngressPublic
)

// Descriptor is one service's registry entry: the facts a node needs before it
// constructs anything.
type Descriptor struct {
	Name     string
	Protocol protocol.ID
	Ingress  Ingress
}

// Build is the public service: forges and people reach it over HTTP, and it holds
// the queue and the job records, serving build.proto behind Protocol to the other
// build nodes and to the runners.
var Build = Descriptor{Name: "build", Protocol: Protocol, Ingress: IngressPublic}

// Runner is the private service that runs builds. Its Protocol is empty: it serves
// nothing and faces nothing, and opens its own stream to build.
var Runner = Descriptor{Name: "runner", Protocol: "", Ingress: IngressNone}
