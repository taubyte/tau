// Package plm holds what plm and the builder both need: the protocol id each serves
// and the service descriptors. The two sit on either side of a trust boundary:
// plm takes webhooks and the job API off the internet and holds the queue and the
// job records, but no clone credentials and no runtime; a builder clones, runs a
// repository's code and publishes what it built, and is reached over p2p alone, by
// plm handing it a run. It imports p2p/rpc and the generated code only, never
// pkg/http.
package plm

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2/builderv2connect"
	"github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2/plmv2connect"
)

// Protocol is "/tau/plm/v2", derived from the schema. Every service block of
// plm.proto mounts behind it.
var Protocol = rpc.Protocol(plmv2connect.QueueServiceName)

// BuilderProtocol is "/tau/builder/v2", what a builder serves.
var BuilderProtocol = rpc.Protocol(builderv2connect.BuilderServiceName)

// QueueDomain names the consistency domain plm's queue lives in, fixed by
// plm.proto. Exported so the composer knows which domain to open.
const QueueDomain = "plm/queue"

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

// PLM is the public service: forges and people reach it over HTTP, and it holds
// the queue and the job records, serving plm.proto behind Protocol to the other
// plm nodes.
var PLM = Descriptor{Name: "plm", Protocol: Protocol, Ingress: IngressPublic}

// Builder is the private service that runs builds, serving builder.proto behind
// BuilderProtocol over p2p and facing nothing else.
var Builder = Descriptor{Name: "builder", Protocol: BuilderProtocol, Ingress: IngressNone}
