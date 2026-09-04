package storage

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	ds "github.com/ipfs/go-datastore"
)

// store.go is the key layout — the ONLY file in this package that builds a
// key. Every other file asks it for a key or a prefix rather than
// concatenating a path itself, so there is exactly one place that has to agree
// with itself about the shape of a key, and one place to read when asking
// whether two rows can collide.
//
// ── THE GRAMMAR ──────────────────────────────────────────────────────────
//
//	/b/<proj>/<env>/<bkt>/<inst>/o/<hexname>/<^stamp:016x>/<writer>  version row
//	/b/<proj>/<env>/<bkt>/<inst>/u/<writer>/added                    uint64
//	/b/<proj>/<env>/<bkt>/<inst>/u/<writer>/removed                  uint64
//	/b/<proj>/<env>/<bkt>/<inst>/u/<writer>/checkpoint               uint64
//	/b/<proj>/<env>/<bkt>/i/<inst>                                   admission row
//
// A segment is a LITERAL spelled out in this file, an IDENTITY through
// segment(), an OBJECT NAME through objectSegment(), or a fixed-width hex
// stamp. Three encodings, and each is chosen for a property the layout rests
// on.
//
// ── IDENTITIES ARE BASE64URL, AND v1 SHOWS WHY ───────────────────────────
//
// segment() is services/v2/auth/store.go's, carried rather than re-derived:
// unpadded base64url, alphabet A-Za-z0-9-_, so an encoded id never contains
// "/" and is never "." or "..". Every boundary in every key is at a fixed
// segment index, whatever the id was.
//
// v1's storage identity is mh.Hash(m.ProjectId + m.ApplicationId + m.Match)
// (services/hoarder/registry.go:44) — an UNSEPARATED CONCATENATION, so
// ("ab","c") and ("a","bc") are one identity, and v2 would add two more fields
// to that concatenation. That is two tenants sharing a namespace, the same
// class as the repository takeover services/v2/auth/store.go records at
// length. Here the four identity parts are four segments and cannot merge at
// any length.
//
// A one-character segment is impossible: base64url of one byte is two
// characters and of zero bytes is the empty string, which is refused. So the
// literals "o", "u" and "i" cannot be produced by segment(), and an instance
// named to look like a literal cannot file its rows in the admission
// namespace. TestALiteralIsUnreachableFromAnEncodedIdentity is that claim.
//
// ── THE OBJECT NAME IS HEX, AND THIS IS THE MEASURED PART ────────────────
//
// An object name is caller-controlled, arbitrary bytes, and it is the one
// input to this layout that nobody vets. Spliced raw it does not survive
// go-datastore's cleaning, and the failures are not exotic — probe-measured on
// this tree, over /b/P/E/B/I/o/<name>:
//
//	"a"                -> /b/P/E/B/I/o/a          )  TWO NAMES, ONE KEY
//	"a/"               -> /b/P/E/B/I/o/a          )
//	"a//b"             -> /b/P/E/B/I/o/a/b        )  TWO NAMES, ONE KEY
//	"a/./b"            -> /b/P/E/B/I/o/a/b        )
//	"a/../b"           -> /b/P/E/B/I/o/b
//	".."               -> /b/P/E/B/I              THE /o/ PREFIX IS GONE
//	"../../etc/passwd" -> /b/P/E/B/etc/passwd     AND SO IS THE INSTANCE
//
// The last two are a namespace escape from a value a guest chooses. Rejecting
// empty, "." and ".." segments does not close it — it does not catch "a/" or
// "a//b" at all — and it makes a security property depend on a validator
// matching every case ds.NewKey cleans, forever.
//
// Hex closes the CLASS. It is fixed-width per byte over an alphabet of
// [0-9a-f], which excludes "/", ".", every separator and every control
// character, so an object name of ANY bytes is exactly one segment that
// ds.NewKey leaves untouched.
//
// AND HEX IS ORDER-PRESERVING WHERE THE RAW NAME IS, which is what makes an
// ordered list under a byte prefix possible at all. Two properties, both
// probe-measured and both tested here:
//
//   - hex(a) < hex(b) exactly when a < b, and hex(p) is a prefix of hex(n)
//     exactly when p is a byte prefix of n — because the encoding is
//     fixed-width and its alphabet is ordered. base64url is neither: it gives
//     up list ordering, which is fatal the moment a bucket sits under a
//     gateway mount and route.strip_prefix makes the path below it the key.
//
//   - IT ALSO SURVIVES THE PHYSICAL LAYOUT UNDERNEATH, which no design in the
//     brief noticed. pkg/kvdb stores an element at <ns>/keys/<key>/v, so the
//     bytes actually compared carry a "/" (0x2f) after the key. For a raw
//     name, a key that is a prefix of another sorts AFTER it whenever the
//     longer name continues with a byte below 0x2f — "a" vs "a-b" ('-' is
//     0x2d) inverts. Every hex digit is 0x30..0x39 or 0x61..0x66, all above
//     0x2f, so the inversion cannot happen for a hex name.
//     TestTheSuffixedPhysicalKeyWouldInvertOrderForRawNames measures both
//     directions.
//
// COST: 2x the key length. Stated, and worth it.
//
// ── THE STAMP IS COMPLEMENTED AND ZERO-PADDED ────────────────────────────
//
// <^stamp:016x> is the bitwise complement of the HLC, sixteen hex digits, so
// byte order over the segment is DESCENDING stamp order and "the current
// version" is the first row under the object's prefix. Zero-padded because
// byte order is the only sort order there is (AGENTS.md §7), and complemented
// because a scan wants newest first. Both are load-bearing and both are
// tested: unpadded, 10 sorts before 2.
//
// ── AND THERE IS NO BUCKET META KEY ──────────────────────────────────────
//
// A bucket's declared shape — its size, its versioning flag, its instance
// limit — comes from the compiled config, not from a row. So the one key every
// writer to a bucket would have contended on does not exist. That is not an
// omission; it is the AGENTS.md §1 checklist answered by not having the key.

const (
	// bucketsRoot is where every replicated row in this service lives.
	bucketsRoot = "/b"

	// objectsSegment holds a bucket instance's version rows.
	objectsSegment = "o"
	// usageSegment holds a bucket instance's per-writer accounting rows.
	usageSegment = "u"
	// instancesSegment holds a BUCKET's admission rows — one per instance,
	// under the bucket rather than under an instance, because instances.limit
	// counts the instances a bucket holds and a row keyed by the instance it
	// is filed under cannot be counted.
	instancesSegment = "i"

	usageAdded      = "added"
	usageRemoved    = "removed"
	usageCheckpoint = "checkpoint"

	// stampWidth is the fixed width of the complemented stamp segment: a
	// uint64 in hex is always sixteen digits.
	stampWidth = 16
)

// segment encodes an identity into exactly one path segment: unpadded
// base64url (RFC 4648 §5), whose alphabet is A-Za-z0-9-_. The encoding of ANY
// string is one segment, cannot be "." or "..", and cannot move a boundary —
// which is why nothing here has to care that go-datastore cleans keys.
func segment(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

// unsegment reverses segment. It fails on a segment this package did not
// write.
func unsegment(s string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("%w: segment %q: %w", ErrNotThisServicesRow, s, err)
	}
	return string(raw), nil
}

// objectSegment encodes an object name into exactly one path segment. See the
// grammar above: this is the encoding that closes the namespace escape, and it
// is the one that keeps `list` ordered.
func objectSegment(name string) (string, error) {
	if name == "" {
		return "", ErrEmptyObjectName
	}
	return hex.EncodeToString([]byte(name)), nil
}

// objectName reverses objectSegment.
func objectName(seg string) (string, error) {
	raw, err := hex.DecodeString(seg)
	if err != nil {
		return "", fmt.Errorf("%w: object segment %q: %w", ErrNotThisServicesRow, seg, err)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("%w: empty object segment", ErrNotThisServicesRow)
	}
	return string(raw), nil
}

// namePrefixSegment encodes a LIST prefix. It differs from objectSegment in
// exactly one way: the empty prefix is legal and means "every object", where
// the empty NAME is not an object at all.
//
// The result is a RAW BYTE PREFIX of the encoded name, not a namespace: hex is
// fixed width, so hex(p) prefixes hex(n) exactly when p prefixes n. A caller
// must therefore match it with a byte-prefix filter and never with
// query.Query.Prefix, which appends "/" and only matches whole segments.
func namePrefixSegment(prefix string) string {
	return hex.EncodeToString([]byte(prefix))
}

// stampSegment is the complemented, zero-padded stamp: the segment that makes
// byte order newest-first.
func stampSegment(stamp uint64) string {
	return fmt.Sprintf("%0*x", stampWidth, ^stamp)
}

// stampFromSegment reverses stampSegment. It insists on the fixed width,
// because an unpadded stamp would sort wrong and a row carrying one was not
// written by this service.
func stampFromSegment(s string) (uint64, error) {
	if len(s) != stampWidth {
		return 0, fmt.Errorf("%w: stamp segment %q is %d characters, not %d", ErrNotThisServicesRow, s, len(s), stampWidth)
	}
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: stamp segment %q: %w", ErrNotThisServicesRow, s, err)
	}
	return ^v, nil
}

// Bucket is a storage resource in one environment: the unit a shard is chosen
// for, and the unit an instance limit counts against.
//
// Its fields are unexported and it is only constructible through NewBucket,
// which validates. The zero Bucket builds no key — see ErrNoAddress — rather
// than building one rooted at "/b///", where every boundary has moved.
type Bucket struct {
	project     string
	environment string
	name        string

	// root is the validated key prefix, computed once. It is empty exactly
	// when this value did not come from NewBucket.
	root string
}

// NewBucket validates and encodes. It is the only door.
//
// THE ENVIRONMENT SEGMENT IS THE ONE THE CALLER PASSES, AND FOR A SHADOW
// ENVIRONMENT THAT MUST BE THE PARENT'S. Nothing in the v2 schema states this:
// PerEnv() on storage's size puts the environment in the runtime key, and
// HANDOVER.md says a shadow shares its parent's state, but the rule that the
// key's environment segment is the parent's is derivable only from
// Environment.Of. It is a decision this package makes and cannot enforce,
// stated here so it is a decision rather than an accident. See doc.go's
// vocabulary findings.
func NewBucket(project, environment, name string) (Bucket, error) {
	if project == "" || environment == "" || name == "" {
		return Bucket{}, fmt.Errorf("%w: bucket (%q, %q, %q)", ErrEmptyIdentity, project, environment, name)
	}
	return Bucket{
		project:     project,
		environment: environment,
		name:        name,
		root:        strings.Join([]string{bucketsRoot, segment(project), segment(environment), segment(name)}, "/"),
	}, nil
}

func (b Bucket) Project() string     { return b.project }
func (b Bucket) Environment() string { return b.environment }
func (b Bucket) Name() string        { return b.name }

// Valid reports whether this value came from NewBucket.
func (b Bucket) Valid() bool { return b.root != "" }

// Instance names one instance of this bucket.
func (b Bucket) Instance(instance string) (Address, error) {
	if !b.Valid() {
		return Address{}, ErrNoAddress
	}
	if instance == "" {
		return Address{}, fmt.Errorf("%w: instance", ErrEmptyIdentity)
	}
	return Address{bucket: b, instance: instance, root: b.root + "/" + segment(instance)}, nil
}

// key is the bucket's own key prefix. Unexported: shard.go hashes it and
// nothing outside this package sees a key.
func (b Bucket) key() string { return b.root }

// instancesPrefix is where a bucket's admission rows live. It ends in "/" so a
// prefix scan cannot reach a sibling whose encoded name merely starts the same
// way.
func (b Bucket) instancesPrefix() (string, error) {
	if !b.Valid() {
		return "", ErrNoAddress
	}
	return b.root + "/" + instancesSegment + "/", nil
}

// admissionKey is the row recording that one instance was admitted. It is
// keyed under the BUCKET so the rows can be counted against instances.limit.
func (b Bucket) admissionKey(instance string) (string, error) {
	prefix, err := b.instancesPrefix()
	if err != nil {
		return "", err
	}
	if instance == "" {
		return "", fmt.Errorf("%w: instance", ErrEmptyIdentity)
	}
	return prefix + segment(instance), nil
}

// instanceFromAdmissionKey inverts admissionKey.
func (b Bucket) instanceFromAdmissionKey(key string) (string, error) {
	prefix, err := b.instancesPrefix()
	if err != nil {
		return "", err
	}
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return "", fmt.Errorf("%w: %q is not an admission row of %s", ErrNotThisServicesRow, key, b.name)
	}
	return unsegment(rest)
}

// Address is one bucket instance: the thing an object key is rooted at.
//
// Like Bucket it is only constructible through a validating door, and the zero
// value builds no key.
type Address struct {
	bucket   Bucket
	instance string
	root     string
}

// NewAddress is the flat door, for a caller holding four wire fields.
func NewAddress(project, environment, bucket, instance string) (Address, error) {
	b, err := NewBucket(project, environment, bucket)
	if err != nil {
		return Address{}, err
	}
	return b.Instance(instance)
}

func (a Address) Bucket() Bucket   { return a.bucket }
func (a Address) Instance() string { return a.instance }

// Valid reports whether this value came from NewAddress or Bucket.Instance.
func (a Address) Valid() bool { return a.root != "" }

// String is for a log line and an error message. It is NOT a key and nothing
// builds one out of it.
func (a Address) String() string {
	if !a.Valid() {
		return "storage:<no address>"
	}
	return fmt.Sprintf("%s/%s/%s/%s", a.bucket.project, a.bucket.environment, a.bucket.name, a.instance)
}

// objectsPrefix is the namespace holding every version row of this instance.
// It is a WHOLE-SEGMENT namespace, so it is the value to pass as a datastore
// query prefix.
func (a Address) objectsPrefix() (string, error) {
	if !a.Valid() {
		return "", ErrNoAddress
	}
	return a.root + "/" + objectsSegment, nil
}

// objectPrefix is the namespace holding every version of ONE object. It is
// also a whole-segment namespace — the object name is exactly one segment —
// which is what makes "the current version" a query with a limit of one
// against a physically scoped scan.
func (a Address) objectPrefix(name string) (string, error) {
	root, err := a.objectsPrefix()
	if err != nil {
		return "", err
	}
	seg, err := objectSegment(name)
	if err != nil {
		return "", err
	}
	return root + "/" + seg, nil
}

// listPrefix is the RAW BYTE prefix that selects every object whose name
// begins with namePrefix. The empty prefix selects the whole instance.
//
// It is raw rather than a namespace on purpose, and a caller must filter with
// it rather than hand it to query.Query.Prefix: an object name is ONE segment,
// so a partial name is a partial segment, and every prefix mechanism in
// go-datastore appends "/" and matches whole segments only. What that costs is
// written down in doc.go, because it is a real cost and not a rounding error.
func (a Address) listPrefix(namePrefix string) (string, error) {
	root, err := a.objectsPrefix()
	if err != nil {
		return "", err
	}
	return root + "/" + namePrefixSegment(namePrefix), nil
}

// versionKey is the row one write produces.
//
// THE WRITER IS IN THE KEY, and that is the whole reason this layout is safe:
// two nodes writing the same object at the same stamp write two DIFFERENT
// keys, so AGENTS.md §1 — "if two nodes can write the same key with different
// content, one write is silently discarded" — holds by construction rather
// than by discipline. v1 left a TODO over a read-modify-write on an LWW CRDT
// asking what happens when two nodes update a version at the same time
// (services/substrate/components/storage/storage/methods.go:79). This is the
// answer: nothing happens, because they are not the same key.
func (a Address) versionKey(name string, stamp uint64, writer string) (string, error) {
	prefix, err := a.objectPrefix(name)
	if err != nil {
		return "", err
	}
	if writer == "" {
		return "", fmt.Errorf("%w: writer", ErrEmptyIdentity)
	}
	return prefix + "/" + stampSegment(stamp) + "/" + segment(writer), nil
}

// usageKey builds one of the three per-writer accounting rows. Each has
// exactly one writer — the peer named in the key — so a read-modify-write on
// it is AGENTS-legal, which is the only reason a counter is allowed to exist
// in a CRDT at all.
func (a Address) usageKey(writer, kind string) (string, error) {
	if !a.Valid() {
		return "", ErrNoAddress
	}
	if writer == "" {
		return "", fmt.Errorf("%w: writer", ErrEmptyIdentity)
	}
	switch kind {
	case usageAdded, usageRemoved, usageCheckpoint:
	default:
		return "", fmt.Errorf("%w: usage row kind %q", ErrNotThisServicesRow, kind)
	}
	return a.root + "/" + usageSegment + "/" + segment(writer) + "/" + kind, nil
}

// usagePrefix is the namespace holding every writer's accounting rows for this
// instance: what a usage sum scans.
func (a Address) usagePrefix() (string, error) {
	if !a.Valid() {
		return "", ErrNoAddress
	}
	return a.root + "/" + usageSegment, nil
}

// VersionKey is a parsed version row key. Parsing exists because a scan
// returns keys and the answer — which object, whose write, at what stamp — is
// entirely in the key. The value is never needed to order two rows.
type VersionKey struct {
	Address Address
	Name    string
	Stamp   uint64
	Writer  string

	// WriterSegment is the ENCODED writer, and it is carried rather than
	// recomputed because it is what the tie-break compares. base64url is not
	// order-preserving, so ordering by Writer and ordering by the key are two
	// different orders, and only one of them is the one every replica sees.
	// See chain.go.
	WriterSegment string
}

// ParseVersionKey inverts versionKey. Every part is checked: a key with the
// wrong depth, a segment that is not base64url, a name that is not hex, a
// stamp that is not sixteen hex digits — each is a row this service did not
// write, and each is an error rather than a value quietly skipped.
func ParseVersionKey(key string) (VersionKey, error) {
	parts := strings.Split(strings.TrimPrefix(key, "/"), "/")
	// b / proj / env / bkt / inst / o / name / stamp / writer
	const want = 9
	if len(parts) != want || "/"+parts[0] != bucketsRoot || parts[5] != objectsSegment {
		return VersionKey{}, fmt.Errorf("%w: %q is not a version row", ErrNotThisServicesRow, key)
	}

	project, err := unsegment(parts[1])
	if err != nil {
		return VersionKey{}, err
	}
	environment, err := unsegment(parts[2])
	if err != nil {
		return VersionKey{}, err
	}
	bucket, err := unsegment(parts[3])
	if err != nil {
		return VersionKey{}, err
	}
	instance, err := unsegment(parts[4])
	if err != nil {
		return VersionKey{}, err
	}
	name, err := objectName(parts[6])
	if err != nil {
		return VersionKey{}, err
	}
	stamp, err := stampFromSegment(parts[7])
	if err != nil {
		return VersionKey{}, err
	}
	writer, err := unsegment(parts[8])
	if err != nil {
		return VersionKey{}, err
	}

	addr, err := NewAddress(project, environment, bucket, instance)
	if err != nil {
		return VersionKey{}, fmt.Errorf("%w: %q: %w", ErrNotThisServicesRow, key, err)
	}
	return VersionKey{
		Address:       addr,
		Name:          name,
		Stamp:         stamp,
		Writer:        writer,
		WriterSegment: parts[8],
	}, nil
}

// clean reports whether a key survives go-datastore's cleaning unchanged. It
// is the property every builder above is written to satisfy, and store_test.go
// drives it over generated identities and object names rather than over a list
// somebody thought of.
func clean(key string) bool { return ds.NewKey(key).String() == key }
