package auth

import "github.com/libp2p/go-libp2p/core/protocol"

// Protocol is the libp2p protocol id this service listens on.
//
// service-conventions.md §2d's rule is mechanical: "/" plus the proto
// package, dots to slashes — so that the id can never drift from the schema
// and can never collide with v1, which the doc's own worked example gives as
// "tau.auth.v1" -> "/tau/auth/v1" alongside v1's live "/auth/v1"
// (services/common/names.go:5).
//
// Applied literally to what is actually committed, it does not hold:
// pkg/v2/proto/auth/v1/auth.proto declares `package auth.v1;`, not
// `tau.auth.v1`. Mechanically deriving from that package name gives
// "/auth/v1" — which IS v1's protocol id, and rpc.Listen on a node running
// both during the coexistence window would fail with rpc.ErrProtocolTaken
// (or worse, whichever service listens second would simply never receive
// anything, which is the exact failure service-conventions.md §2d cites
// go-libp2p-gostream's protocol registration as silently allowing). This is
// flagged rather than silently resolved: the fix belongs in the .proto's
// package declaration, and this agent's brief is to implement the generated
// interfaces, not edit the schema ("Do not change the proto; if it is wrong,
// report that rather than editing it"). Until the package is renamed, this
// constant supplies the "tau/" segment the doc's own anti-collision rule
// requires, spelled out rather than mechanically walked off
// authv1connect.ProjectServiceName (which carries the RPC *service* name,
// "auth.v1.ProjectService" — one of five in this schema — not the proto
// *package*).
const Protocol = protocol.ID("/tau/auth/v1")

// Ingress states which side of the trust boundary a service is on
// (service-conventions.md §5a).
type Ingress int

const (
	// IngressNone is p2p-only, and the default so silence is safe.
	IngressNone Ingress = iota
	// IngressPublic binds pkg/http and faces the internet.
	IngressPublic
)

// descriptor is auth's registry entry: name, protocol id and trust posture —
// the three facts a node needs before it constructs anything.
type descriptor struct {
	Name     string
	Protocol protocol.ID
	Ingress  Ingress
}

// Descriptor states that auth answers other tau nodes only. Per
// .notes/v2/service-topology.md, gateway and the build frontend are the only
// two services meant to be publicly reachable; this package never imports
// pkg/http and has no ingress.go, which is the second, mechanical half of
// that check (service-conventions.md §5a) — grep for pkg/http in this
// package and find nothing.
//
// service-conventions.md §2d's own example additionally carries a `New`
// closure adapting this into a shared `service.Descriptor`/`service.Config`
// pair for a node registry to range over (`New: func(ctx, cfg
// service.Config) (service.Service, error) { return New(ctx, cfg) }`). No
// such registry exists anywhere in the tree yet — auth is the first v2
// service to be written — so there is nothing for that closure to satisfy.
// Descriptor stays plain data until a registry shows up for it to register
// with; adding the closure now would be guessing at that registry's shape,
// which is exactly the kind of invention service-conventions.md §8 leaves
// open rather than made here.
var Descriptor = descriptor{
	Name:     "auth",
	Protocol: Protocol,
	Ingress:  IngressNone,
}
