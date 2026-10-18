// Package build holds the two facts both of build's roles need and neither
// owns: the protocol id the backend serves and the frontend dials, and the
// descriptors a node registry will one day range over.
//
// v1's patrick (the webhook listener and the job store) and monkey (the CI
// runner) merge into one v2 service split into two ROLES on either side of a
// TRUST boundary rather than a scaling one (.notes/v2/service-topology.md §2,
// and build.proto's package doc at length):
//
//   - the FRONTEND takes webhooks off the internet and answers the
//     GitHub-token-authenticated job API. It holds no clone credentials, no
//     container runtime and no publish rights, and it serves no p2p RPC.
//   - the BACKEND owns the queue, the job records and the run. It is
//     reachable only over p2p, by peers libp2p has already authenticated.
//
// It diverges from services/auth, which keeps `descriptor` unexported with
// one exported var in its own package. auth is one role behind one protocol
// id; build is two roles behind one, so a per-role descriptor.go would type
// that id twice and give service-conventions.md §5a's two-public-services
// test two places to look. It imports p2p/rpc and the generated connect
// package and nothing else — in particular no pkg/http, so the backend's
// IngressNone import check stays mechanical.
package build

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
)

// Protocol is "/tau/build/v2" — walked off the schema, never typed
// (service-conventions.md §2d). rpc.Protocol(QueueServiceName) and
// rpc.Protocol(JobServiceName) both return it, because the id names the proto
// PACKAGE's listener and both service blocks mount behind one mux; build_test.go
// holds that rather than this comment.
//
// The "tau." segment is what keeps this off v1's live "/patrick/v1" and
// "/monkey/v1" (services/common/names.go) for the whole span in which v1 and
// v2 run in one swarm and one binary.
var Protocol = rpc.Protocol(buildv2connect.QueueServiceName)

// QueueDomain names the consistency domain build's queue lives in — the one
// build.proto fixes by literal ("a backend node claims its own work by joining
// the \"build/queue\" consistency domain directly") and the string a node's
// Groups.Join will one day be handed (service-conventions.md §4). Nothing
// implements Join yet, so the backend takes its store as a constructor
// argument; this constant is exported so whatever composes the node knows
// which domain to open for it.
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

// Descriptor is one role's registry entry: the three facts a node needs
// before it constructs anything.
//
// service-conventions.md §2d's own example additionally carries a `New`
// closure adapting this into a shared registry's Descriptor/Config pair. No
// such registry exists anywhere in the tree — services/auth's descriptor
// declines to guess its shape for the same reason — so these stay plain data.
type Descriptor struct {
	Name     string
	Protocol protocol.ID
	Ingress  Ingress
}

// Frontend's Protocol is EMPTY, and the emptiness is the trust boundary
// written as data: build.proto's header says this role hosts no RPC surface
// of its own ("an empty surface is a strong claim and it is meant as one"), so
// it has no protocol id to name. A registry consuming these must permit
// ingress without a protocol id, which is the shape that makes "the public
// role cannot be dialled by the swarm" a fact a test can read rather than a
// convention.
var Frontend = Descriptor{Name: "build-frontend", Protocol: "", Ingress: IngressPublic}

// Backend serves both of build.proto's service blocks behind Protocol, and
// faces no internet: every caller it has is a peer libp2p authenticated.
var Backend = Descriptor{Name: "build-backend", Protocol: Protocol, Ingress: IngressNone}
