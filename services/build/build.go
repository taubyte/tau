// Package build holds what both build services need: the protocol id build serves
// and a runner dials, the service descriptors, and how a run's log becomes the
// events a follower reads (lines.go). The two sit on either side of a trust
// boundary: build takes webhooks and the job API off the internet and holds the
// queue and the job records, but no clone credentials and no runtime; a runner
// clones, runs a repository's code and publishes what it built, is reachable by
// nothing, and dials build to be handed work. It imports p2p/rpc and the
// generated code only, never pkg/http.
package build

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
)

// Protocol is "/tau/build/v2", derived from the schema. Every service block of
// build.proto mounts behind it.
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
