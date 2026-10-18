package auth

import (
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// Protocol is the libp2p protocol id this service listens on: "/" plus the
// proto package with dots as slashes, walked off the schema rather than
// typed here (service-conventions.md §2d, rpc.Protocol). Any of the five
// generated service names gives the same answer, because the id names the
// package's listener and all five mount behind it.
//
// The "tau." prefix in that package keeps the id from being a bare service
// word; pkg/proto's rules test holds it for every package.
var Protocol = rpc.Protocol(authv2connect.ProjectServiceName)

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
