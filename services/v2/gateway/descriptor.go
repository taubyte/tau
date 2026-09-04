package gateway

// Ingress states which side of the trust boundary a service is on.
type Ingress int

const (
	// IngressNone is p2p-only.
	IngressNone Ingress = iota
	// IngressPublic faces the internet.
	IngressPublic
)

type descriptor struct {
	Name    string
	Ingress Ingress
}

// Descriptor states that a gateway is public, and it is one of the two services
// that are.
//
// IT HAS NO PROTOCOL ID because it answers no peer: it is the direction of
// travel reversed. Every other v2 service listens on a libp2p stream and is
// reached by other nodes; this one listens on the internet and reaches them.
// That is also why it takes no host.Host — what it needs is something that can
// invoke, and how that reaches a worker is the composer's.
var Descriptor = descriptor{
	Name:    "gateway",
	Ingress: IngressPublic,
}
