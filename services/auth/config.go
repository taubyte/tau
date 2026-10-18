package auth

import (
	"context"

	"github.com/taubyte/tau/core/kvdb"
)

// config.go declares what this service is built from, and it declares three
// separate things rather than one bag (.notes/v2/service-conventions.md,
// "Config, node and resources are three things"):
//
//  1. CONFIG — Config below. What an operator wrote down. A plain immutable
//     value: no setters, no live objects, nothing that had to be
//     constructed, and it survives a round trip through yaml.
//  2. THE NODE — passed to New explicitly, as host.Host. Not a peer.Node:
//     this service only talks, so the narrowest thing it can be built and
//     tested against is a host, and a host is what mocknet.GenPeer() hands
//     back with nothing else standing up (service_test.go's
//     TestServiceNeedsOnlyAMocknetHostAndAnInMemoryStore is that claim as a
//     test).
//  3. RESOURCES — Store below, injected as an interface by whoever composes
//     the node. Never a kvdb.Factory the service opens a database out of:
//     that made the service the owner of a resource's lifetime it did not
//     create, and made "can I construct this?" depend on a factory existing.
//
// The first pass took all three off one config-shaped interface with a
// Node() peer.Node method on it — v1's god-object, inherited on day one.
// peer.Node bundles host, DHT, datastore, DAG and pubsub, so a test that
// wanted a peer got a DHT bootstrap and a real on-disk datastore in a temp
// dir with it (p2p/peer/mock.go). Nothing in this package ever asked for
// any of that.

// Config is what an operator declared for this service.
//
// It holds one thing today, and that is not an accident of scope: a
// certificate lives in the store, a repository's ownership is decided by the
// transport, and the protocol id is derived from the schema (descriptor.go),
// so the domain-validation key pair is genuinely the only operator-supplied
// value auth reads. Anything added here later has to pass the same test —
// if an operator cannot write it down, it is not config.
type Config struct {
	// DomainValidation is the key pair DomainService.Register signs
	// domain-ownership claims with (github.com/taubyte/domain-validation,
	// v1's services/auth/helpers.go domainValidationNew). Both halves are
	// required; New refuses a Config missing either.
	DomainValidation KeyPair
}

// KeyPair is a PEM key pair as an operator wrote it: text, not parsed key
// material, and strings rather than []byte so a Config handed to New cannot
// be edited afterwards through the slice the caller still holds.
type KeyPair struct {
	Private string
	Public  string
}

// Store is the slice of kvdb.KVDB this service actually uses. Declaring it
// here rather than taking kvdb.KVDB whole is the consumer-side interface
// idiom, and it buys two things beyond a smaller test double:
//
//   - There is no Close. The store is handed to New by a composition root
//     that owns its lifetime, and a service that cannot name Close cannot
//     close a database three other services are still reading
//     (service-conventions.md §2e, "anything it was handed, it does not").
//     That is structural here, not a promise in a comment.
//   - There is no Factory, no Sync and no Stats, so nothing in this package
//     can reach sideways out of its own database.
//   - There is no List either, only ListRegEx. Every listing this service
//     makes is an ANCHORED match (store.go), and a plain prefix listing is
//     exactly the shape that made a repository takeover possible, so the
//     interface does not offer one.
//
// kvdb.KVDB satisfies it structurally, so the node passes the database it
// already opened.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, v []byte) error
	Delete(ctx context.Context, key string) error
	ListRegEx(ctx context.Context, prefix string, regexs ...string) ([]string, error)
	Batch(ctx context.Context) (kvdb.Batch, error)
}

var _ Store = (kvdb.KVDB)(nil)

// GenerateKeyPair mints a domain-validation key pair.
//
// IT IS EXPORTED FOR WHATEVER STARTS THIS SERVICE. A cloud that has never been
// given one still has to start — a single-node development cloud is the whole
// point of a default shape — and the alternative is either refusing to boot or
// a key baked into the binary, which would let every cloud in the world sign
// for every other cloud's domains.
//
// A MINTED PAIR IS NOT A SUBSTITUTE FOR ONE AN OPERATOR KEEPS. Domain-ownership
// claims are signed with it, so a cloud that mints a new one on each start
// stops being able to verify what it verified yesterday. Whoever mints one is
// expected to say so loudly.
func GenerateKeyPair() (KeyPair, error) {
	private, public, err := generateECKeyPair()
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{Private: private, Public: public}, nil
}
