package engine

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"strings"

	mh "github.com/multiformats/go-multihash"
)

// Generated fields: values a consumer must MINT rather than ask the user for —
// a resource id today. The DSL says which fields those are and which known
// generator makes them, so tau-cli and the web console stop each carrying their
// own "how do I mint an id" (both currently do, and they disagree on the seed).

// Known generator ids. A generated field names one of these; a consumer that
// doesn't recognize the id treats the field as read-only and leaves it alone.
const (
	// DeriveResourceID is the id of a resource nobody authored: what a lowering
	// produces. Same inputs, same id, forever — see derivedCID.
	DeriveResourceID = "resource-id"

	// GenCID mints a fresh CIDv0-shaped id: a base58 sha2-256 multihash over the
	// seed plus fresh randomness. Unique per call, and parses as a CID (see IsCID).
	GenCID = "cid"
)

// Generated declares that this attribute's value is minted by a known generator
// rather than authored — a UI renders it read-only and fills it via Generate.
// Schema/tooling only: no compile, parse or validation effect (the field keeps
// whatever validator it declares).
func Generated(kind string) Option { return Annotate("generated", kind) }

// GeneratedBy reports the generator id declared for a field of a resource group,
// or "" when the field is authored (or unknown).//
// Addresses a field of a GROUP. The project root's own attributes are not reachable
// through it — findAttr walks groups — so the project id's generator and the project
// label's derivation are both invisible here. Pre-existing, not introduced by the
// derivation: a consumer that needs a root field reads Root().Attributes directly.

func GeneratedBy(root []*Node, group string, field []string) string {
	a := findAttr(root, group, field)
	if a == nil {
		return ""
	}
	kind, _ := a.Meta["generated"].(string)
	return kind
}

// Generate mints a value for a generated field. seed is the caller's context
// (e.g. the resource's address); a generator MUST mix in its own entropy rather
// than rely on it, so two resources that share a seed still never collide and
// re-generating one never reproduces the old value.
func Generate(kind string, seed ...any) (string, error) {
	switch kind {
	case GenCID:
		return newCID(seed...)
	case "":
		return "", errors.New("not a generated field")
	}
	return "", fmt.Errorf("unknown generator %q", kind)
}

// Derived fields: values that are a PURE FUNCTION of the document — same input, same
// output, forever. The sibling of Generated, and deliberately not the same thing:
//
//	Generated  mints. Fresh entropy every call, never reproducible, unique per
//	           resource. An id.
//	Derived    computes. No entropy at all, and reproducing it is the entire point.
//	           A name that has to be the same everywhere the project is read.
//
// Running one through the other's helper would be a bug in whichever direction it
// went: a minted id that reproduced would collide, and a derived name that did not
// would resolve differently in two consumers.
//
// Same reason both exist here rather than in each consumer: tau-cli, the web console
// and seer all need a project's zone label, and three implementations of "hash the id
// and encode it" is three chances to disagree about a name that must match.

// Known derivation ids.
const (
	// DeriveSRVName is the DNS name a client looks up to find a service's port: the
	// RFC 2782 form, `_service._protocol`. See srvName.
	DeriveSRVName = "srv-name"

	// DeriveDNSLabel turns an identifier into one DNS label: sha2-256, base32, 13
	// characters. See dnsLabel for why base32 and why 13.
	DeriveDNSLabel = "dns-label"
)

// Derived declares that an attribute's value is computed from the named sibling
// fields rather than authored — a UI renders it read-only and fills it via Derive.
// Schema/tooling only: no compile, parse or validation effect.
func Derived(kind string, from ...string) Option {
	return func(a *Attribute) {
		Annotate("derived", kind)(a)
		Annotate("derivedFrom", from)(a)
	}
}

// DerivedBy reports the derivation id declared for a field of a resource group, and
// the fields it is computed from. Empty kind when the field is authored.//
// Addresses a field of a GROUP. The project root's own attributes are not reachable
// through it — findAttr walks groups — so the project id's generator and the project
// label's derivation are both invisible here. Pre-existing, not introduced by the
// derivation: a consumer that needs a root field reads Root().Attributes directly.

func DerivedBy(root []*Node, group string, field []string) (kind string, from []string) {
	a := findAttr(root, group, field)
	if a == nil {
		return "", nil
	}
	kind, _ = a.Meta["derived"].(string)
	from, _ = a.Meta["derivedFrom"].([]string)
	return kind, from
}

// Derive computes a derived field from its inputs. Unlike Generate it takes no
// entropy and returns the same value for the same inputs, on every machine, forever.
func Derive(kind string, from ...string) (string, error) {
	switch kind {
	case DeriveDNSLabel:
		return dnsLabel(from...)
	case DeriveResourceID:
		return derivedCID(from...)
	case DeriveSRVName:
		return srvName(from...)
	case "":
		return "", errors.New("not a derived field")
	}
	return "", fmt.Errorf("unknown derivation %q", kind)
}

// dnsLabelLength is set by the birthday bound, not by how many projects there are.
// With N identifiers in a space of S the chance of any collision is about N²/2S:
//
//	 8 chars = 40 bits = 1.1e12    at 1M: ~1 in 2        — unusable
//	10 chars = 50 bits = 1.1e15    at 1M: ~1 in 2,200
//	13 chars = 65 bits = 3.7e19    at 1M: ~1 in 74,000,000
//
// 13 leaves room for two orders of magnitude past "millions" and spends 13 of the 63
// characters a DNS label allows, which nothing else is competing for.
const dnsLabelLength = 13

// BASE32, not base62. DNS labels are CASE-INSENSITIVE (RFC 4343), so "Ab" and "aB"
// are the same name: an encoding spending upper and lower case would not have 62
// symbols, it would have 36 with an invisible collision between every pair differing
// only in case — the worst kind, because the alphabet claims it cannot happen.
//
// Base32 (RFC 4648) is the standard answer, and the reason IPFS subdomain gateways
// serve <cidv1base32>.ipfs.dweb.link rather than the base58 a CID is normally written
// in.
var dnsLabelEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// The input is HASHED rather than re-encoded. A CID's leading bytes are its multihash
// prefix and codec — identical for every id minted the same way — so a prefix of one
// would be a constant. Hashing is what makes every character carry entropy.
func dnsLabel(from ...string) (string, error) {
	if len(from) == 0 || from[0] == "" {
		return "", errors.New("dns-label needs an identifier to derive from")
	}
	sum := sha256.Sum256([]byte(strings.Join(from, "\x00")))
	return dnsLabelEncoding.EncodeToString(sum[:])[:dnsLabelLength], nil
}

// srvName is RFC 2782's `_service._protocol`, which is what a client actually looks up
// when the port is not something it may assume.
//
// DERIVED rather than left to whoever is displaying it. A view was composing this out of
// two fields, which made a fact about what a client must DO into a decoration invented
// by a renderer: it appeared in one drawing and nowhere else, and nothing else that
// needed the name — a CLI, a console, the runtime publishing the record — could have
// agreed with it except by coincidence.
func srvName(from ...string) (string, error) {
	if len(from) < 2 || from[0] == "" || from[1] == "" {
		return "", errors.New("an SRV name needs a service and a protocol")
	}
	return "_" + from[0] + "._" + from[1], nil
}

// derivedCID is GenCID without the randomness, and the difference is the whole point.
//
// A resource a compiler wrote has to have the SAME id every time the config is
// compiled: it is stored under that id, referred to by it, and re-minting it on every
// compile would make each build look like a delete and a create. So it is a pure
// function of what produced it — the source resource's id, the kind, and the part —
// which also means it survives a rename of anything that is not one of those, and that
// two clouds compiling the same project agree without coordinating.
//
// Same multihash shape as a minted id (sha2-256, base58), so nothing downstream can
// tell the two apart by looking, and IsCID accepts it.
func derivedCID(from ...string) (string, error) {
	if len(from) == 0 {
		return "", errors.New("a derived id needs something to derive from")
	}
	// NUL-joined: two different splits of the same characters must not collide, which
	// concatenation alone would allow (a+bc == ab+c).
	hash, err := mh.Sum([]byte(strings.Join(from, "\x00")), mh.SHA2_256, -1)
	if err != nil {
		return "", err
	}
	return hash.B58String(), nil
}

func newCID(seed ...any) (string, error) {
	r := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, r); err != nil {
		return "", fmt.Errorf("generating randomness failed: %w", err)
	}
	hash, err := mh.Sum(fmt.Append(nil, append(seed, r)...), mh.SHA2_256, -1)
	if err != nil {
		return "", err
	}
	return hash.B58String(), nil
}
