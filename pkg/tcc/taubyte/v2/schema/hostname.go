package schema

import (
	"crypto/sha256"
	"encoding/base32"
	"strings"
)

// The label a project occupies in the cloud's generated zone.
//
//	<name>.<ProjectLabel(id)>.<cloud fqdn>
//	 db     f3k2m9x7q4wtb      tau.somecloud.io
//
// It is a pure function of the project id, which is a CID and therefore already
// unique — so the middle label is DERIVED, not assigned, and the same project has the
// same label on every cloud that hosts it. Only the cloud's own fqdn is still
// resolved at deploy.
//
// # Why base32 and not base62
//
// DNS labels are CASE-INSENSITIVE (RFC 4343): `Ab` and `aB` are the same name. An
// encoding using upper and lower case would therefore not have 62 distinct symbols, it
// would have 36 with an invisible collision between every pair that differs only in
// case — which is the worst kind of collision, because it is one the alphabet claims
// cannot happen.
//
// So the usable alphabet is letters-and-digits, case-folded. Base32 (RFC 4648) is the
// standard choice for exactly this and for exactly this reason: it is why IPFS
// subdomain gateways serve `<cidv1base32>.ipfs.dweb.link` rather than the base58 the
// CID is normally written in. Base36 would be marginally denser and needs bignum
// division; base32 is five bits a character, a shift and a mask.
//
// # Why 13 characters
//
// Collisions are what set the length, not the number of projects — the birthday bound
// is what bites. With N projects in a space of S labels the probability of any
// collision is about N²/2S:
//
//	 8 chars = 40 bits = 1.1e12    at 1M projects: ~1 in 2       — unusable
//	10 chars = 50 bits = 1.1e15    at 1M projects: ~1 in 2200
//	13 chars = 65 bits = 3.7e19    at 1M projects: ~1 in 74 million
//	13 chars                       at 100M projects: ~1 in 7400
//
// 13 gives room for a cloud two orders of magnitude larger than "millions" and still
// leaves the collision rarer than the disk losing the record. It costs 13 of the 63
// characters a DNS label allows, which nothing else is competing for.
//
// A collision is not silent if it ever happens: two projects would claim one label in
// one cloud's zone, which is a registration conflict that cloud can see and refuse.
// The length is what makes that path cold, not what makes it impossible.
const ProjectLabelLength = 13

var labelEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// ProjectLabel derives a project's label in a cloud's generated zone from its id.
//
// The id is hashed rather than re-encoded: a CID carries a multihash prefix and a
// codec, so its leading bytes are IDENTICAL across projects and a prefix of it would
// collide on every project made the same way. Hashing first is what makes every
// character carry entropy.
func ProjectLabel(projectID string) string {
	sum := sha256.Sum256([]byte(projectID))
	label := labelEncoding.EncodeToString(sum[:])
	return label[:ProjectLabelLength]
}

// GeneratedHost spells out what a resource is reachable at, as far as the config can
// know it. The cloud's own fqdn is not in the config and is not guessed here — the
// caller that knows it substitutes the last segment.
//
//	GeneratedHost("db", "Qm…") == "db.f3k2m9x7q4wtb"
//	GeneratedHost("",   "Qm…") == "f3k2m9x7q4wtb"
func GeneratedHost(name, projectID string) string {
	label := ProjectLabel(projectID)
	if name == "" {
		return label
	}
	return strings.Join([]string{name, label}, ".")
}
