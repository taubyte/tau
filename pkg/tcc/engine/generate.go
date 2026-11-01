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

// Generated fields are values a consumer mints rather than asks for, such as a
// resource id; the DSL names the generator so consumers agree.

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

// GeneratedBy reports the generator declared for a field of a resource group,
// or "" when authored. The project root's own attributes are not reachable
// through it; read Root().Attributes.

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

// Derived fields are a pure function of the document; Generated ones mint fresh
// entropy. A minted id that reproduced would collide, and a derived name that
// did not would resolve differently in two consumers.

const (
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

// Derive computes a derived field from its inputs. Unlike Generate it takes no
// entropy and returns the same value for the same inputs, on every machine, forever.
func Derive(kind string, from ...string) (string, error) {
	switch kind {
	case DeriveDNSLabel:
		return dnsLabel(from...)
	case DeriveResourceID:
		return derivedCID(from...)
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

// Base32, not base62: DNS labels are case-insensitive (RFC 4343), so a
// case-spending alphabet collides invisibly.
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

// derivedCID is GenCID without the randomness: a compiler-written resource must
// keep its id across compiles, so it is a pure function of the source resource's
// id, the kind and the part. Same multihash shape as a minted id.
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
