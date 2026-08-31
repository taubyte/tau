package auth

import (
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
)

// Config is the narrow slice of node configuration this service actually
// reads — not pkg/config.Config's forty-odd methods and six setters
// (service-conventions.md §2b). A test double for it is four fields, which
// is most of what makes the >90% coverage bar in §6 reachable.
//
// *pkg/config.config satisfies every method below except DomainValidation:
// that one is spelled as two byte slices here rather than pkg/config's own
// named DomainValidation struct, so this package does not have to import
// pkg/config for a single type. Wiring that one method at the node-bootstrap
// layer is the kind of adapter service-conventions.md §8 leaves open
// ("Whether v2's node keeps that pattern or hands constructed dependencies
// forward explicitly is a node-bootstrap decision, not a service-shape
// one") — not solved here, because this package owns services/v2/auth and
// nothing upstream of it.
type Config interface {
	// Root is the node's data directory. Nothing under services/v2/auth
	// reads it today — every row this service owns lives in the kvdb, not on
	// disk — but it is kept on the interface because dropping it now would
	// just have it added back the first time a certificate cache or a
	// domain-validation key needs to be read from Root by whatever
	// constructs Config, and a Config a caller cannot satisfy without an
	// unused method is cheaper to keep than to re-add.
	Root() string

	// DevMode changes nothing about storage shape or authorization. It only
	// feeds Databases' rebroadcast interval — 5s in production, 1s in dev,
	// the same numbers services/auth/service.go:60-63 used — because a
	// developer iterating locally would otherwise wait out a real
	// rebroadcast window on every restart.
	DevMode() bool

	// Node is this node's libp2p identity. It is required: New refuses a nil
	// Node rather than inventing one, because a service that can invent its
	// own identity can be started two ways and one of them is never how
	// production starts it (service-conventions.md §2b).
	Node() peer.Node

	// Databases opens this service's own store. The factory is shared with
	// every other service on the node — New opens exactly one database from
	// it, under this service's own name, and Close closes only that one
	// database, never the factory (see service.go).
	Databases() kvdb.Factory

	// DomainValidation is the P-256 key pair DomainService.Register signs
	// domain-ownership claims with (services/auth/helpers.go's
	// domainValidationNew, github.com/taubyte/domain-validation). Both
	// halves are required: New refuses to start without them, the same way
	// v1 refused when a node-supplied config carried empty keys
	// (services/auth/service.go:41-44).
	DomainValidation() (privateKey, publicKey []byte)
}
