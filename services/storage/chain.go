package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/fxamacker/cbor/v2"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

// chain.go is the mutable pointer, and the point of the file is that THERE IS
// NO POINTER.
//
// "The current version of object X" is not a key anybody overwrites. It is a
// DERIVED MAXIMUM over an append-only, per-writer-disjoint version list:
//
//	/b/<proj>/<env>/<bkt>/<inst>/o/<hexname>/<^stamp>/<writer> -> VersionRecord
//
// Three properties, each doing exactly one job:
//
//	THE WRITER IS IN THE KEY      so two nodes cannot write one key with
//	                              different content. AGENTS.md §1 holds by
//	                              construction rather than by discipline.
//	THE STAMP IS COMPLEMENTED     so byte order is newest-first and "current"
//	                              is one row, not a sort over a chain.
//	THE VALUE CARRIES EVERYTHING  so resolution costs one row and never a
//	  head NEEDS                  block fetch.
//
// RESOLUTION, IN FULL: scan the object's prefix, lowest key first, take one
// row. If it is a tombstone the object is gone. No rows means gone. That is
// it — no coordination, no lock, no leader, and no comparison of one node's
// clock against another's. Only byte order over converged state.
//
// This is AGENTS.md §3 — make conflict visible, then resolve it
// deterministically — with the ordering value moved OUT OF THE ENTRY AND INTO
// THE KEY, which turns an O(chain) sort into an O(1) scan.
//
// ── WHY NOT THE OBVIOUS ALTERNATIVES ─────────────────────────────────────
//
// THE CID AS THE VERSION. Tempting: identical bytes are one key, so a retry is
// free. Refused because there is no order over CIDs meaning "later", so
// `current` would be undefined. The CID rides in the VALUE, the order rides in
// the KEY, and idempotence comes back anyway — a retry carries the same
// (stamp, writer) and therefore writes the same key.
//
// kvdb's OWN MERGE RULE. Highest DAG-height priority wins, greater value bytes
// break the tie (pkg/kvdb/set.go findBestValue). Deterministic, clock-free,
// and it would almost work for /o/<name> -> cid. Refused for one reason: ON A
// TIE ONE WRITE IS SILENTLY DISCARDED, with no error to either caller. For an
// object store that is losing a PUT that returned success.
//
// A DELETE IS A ROW. A tombstone version, with its own stamp, acked on K
// exactly like a put — never a kvdb.Delete on the pointer, because AGENTS.md
// §5 says a delete is an LWW write too and a delete racing a re-create can
// lose. On a version list both survive and the later stamp wins, visibly.
//
// ── AND THE ORDER THE KEYS PRODUCE IS THE ORDER Newest COMPUTES ──────────
//
// Two orders exist here and they must be the same one: the byte order a store
// returns keys in, and the comparison Newest makes over parsed rows. They
// agree on the stamp trivially. They agree on the TIE-BREAK only because
// Newest compares the ENCODED writer segment — base64url is not
// order-preserving, so ordering by the writer's own bytes is a DIFFERENT
// order from ordering by the key, and a node using one while its neighbour
// used the other is exactly the split the whole design exists to prevent.
// TestTheKeyOrderAndTheResolutionOrderAreOneOrder drives both over generated
// writers and asserts they never disagree.

// VersionRecord is the value of a version row: everything head needs, so
// resolution never fetches a block.
//
// cbor with integer keys, which is what the rest of v2 stores its rows in: it
// never crosses a wire and no other language reads it. What crosses a wire is
// protobuf, and this is not that.
type VersionRecord struct {
	// RootCID is the UnixFS root of the object's bytes. It is an ADDRESS, not
	// a credential — a caller is authorized on the bucket, never on knowing
	// this. It never crosses the RPC boundary to a guest.
	RootCID string `cbor:"1,keyasint"`

	// Size is the object's byte length, carried so head and the usage sum
	// never touch a block.
	Size uint64 `cbor:"2,keyasint"`

	ContentType string `cbor:"3,keyasint,omitempty"`

	// CreatedMS is wall-clock milliseconds, for display. NOTHING DECIDES
	// ANYTHING FROM IT: ordering is the stamp, and the grace window is timed
	// from local observation. It is in the record because head reports it.
	CreatedMS uint64 `cbor:"4,keyasint"`

	// Writer names the node that wrote this row. It duplicates the key's last
	// segment on purpose: the key is authoritative — it is what makes the row
	// uncontended — and a record disagreeing with its own key is a bug in
	// whoever wrote it, caught on decode rather than resolved as a conflict.
	Writer string `cbor:"5,keyasint"`

	// Tombstone marks this version as a delete. The row is a row like any
	// other: it has a stamp, it is acked on K, and a re-create with a later
	// stamp wins over it visibly.
	Tombstone bool `cbor:"6,keyasint,omitempty"`

	// Exclusive records that every block of this object was new to this bucket
	// instance when it was imported. It is the GC fast path: deleting an
	// exclusive object frees its DAG with no mark phase at all. It is only
	// ever a HINT THAT SAVES WORK — false means "walk", never "keep".
	Exclusive bool `cbor:"7,keyasint,omitempty"`
}

func (r VersionRecord) encode() ([]byte, error) { return cbor.Marshal(r) }

func decodeVersionRecord(raw []byte) (VersionRecord, error) {
	var r VersionRecord
	if err := cbor.Unmarshal(raw, &r); err != nil {
		return VersionRecord{}, fmt.Errorf("%w: %w", ErrNotThisServicesRow, err)
	}
	return r, nil
}

// Version is one row, key and value together. It is what every reader in this
// service works with, and it is entirely derivable from the row — nothing here
// is carried from the request that produced it.
type Version struct {
	VersionKey
	Record VersionRecord
}

// Tombstone reports whether this version is a delete.
func (v Version) Tombstone() bool { return v.Record.Tombstone }

// parseVersion turns one scanned row into a Version, checking that the record
// and its key agree about who wrote it.
func parseVersion(key string, value []byte) (Version, error) {
	vk, err := ParseVersionKey(key)
	if err != nil {
		return Version{}, err
	}
	rec, err := decodeVersionRecord(value)
	if err != nil {
		return Version{}, fmt.Errorf("%w: %q: %w", ErrNotThisServicesRow, key, err)
	}
	if rec.Writer != "" && rec.Writer != vk.Writer {
		return Version{}, fmt.Errorf("%w: key says %q, record says %q", ErrWriterMismatch, vk.Writer, rec.Writer)
	}
	return Version{VersionKey: vk, Record: rec}, nil
}

// Newest is the resolution rule as a pure function: greatest stamp wins, and a
// tie is broken by the ENCODED writer segment ascending — which is what byte
// order over the keys does for free, because the writer is the next segment
// after the stamp.
//
// It exists separately from the scan so the rule can be tested against
// generated inputs with no store at all, and so no caller writes it slightly
// differently. Sorting rather than a single pass because callers also want the
// full ordered list (Versions), and one order is cheaper to keep correct than
// two.
func Newest(vs []Version) (Version, bool) {
	if len(vs) == 0 {
		return Version{}, false
	}
	ordered := Order(vs)
	return ordered[0], true
}

// Order returns the versions newest-first, in the SAME order the keys sort in.
// It copies: a resolution function that reorders its caller's slice makes the
// caller's second call depend on its first.
func Order(vs []Version) []Version {
	out := make([]Version, len(vs))
	copy(out, vs)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Stamp != out[j].Stamp {
			return out[i].Stamp > out[j].Stamp
		}
		return out[i].WriterSegment < out[j].WriterSegment
	})
	return out
}

// index is the shard store as this service reads and writes it. It is the only
// thing in this package that builds a datastore query.
type index struct{ store Datastore }

// row is one scanned key and value.
type row struct {
	key   string
	value []byte
}

// first returns the lowest-keyed row under a whole-segment namespace.
//
// IT ASKS FOR THE ORDER EXPLICITLY. go-datastore does not promise an order
// without one, and the difference is not theoretical: the same probe that
// returned the newest version from pebble returned rows in ARBITRARY order
// from ds.MapDatastore, and its "limit 1" answer changed between runs. Since
// pkg/kvdb's own harness defaults to the map store, a suite that copied it
// would pass while testing nothing about the property this whole design rests
// on. OrderByKey costs pebble nothing — go-ds-pebble turns it into iter.First
// plus iter.Next, its natural scan — and it makes the answer independent of
// the backend rather than dependent on a contract nobody wrote down.
func (i index) first(ctx context.Context, namespace string) (row, bool, error) {
	res, err := i.store.Query(ctx, query.Query{
		Prefix: namespace,
		Orders: []query.Order{query.OrderByKey{}},
		Limit:  1,
	})
	if err != nil {
		return row{}, false, err
	}
	defer res.Close()

	e, ok := res.NextSync()
	if !ok {
		return row{}, false, nil
	}
	if e.Error != nil {
		return row{}, false, e.Error
	}
	return row{key: e.Key, value: e.Value}, true, nil
}

// all returns every row under a whole-segment namespace, in key order.
func (i index) all(ctx context.Context, namespace string, limit int) ([]row, error) {
	return i.scan(ctx, namespace, "", "", limit)
}

// scan returns rows under a whole-segment namespace whose keys also begin with
// the RAW BYTE prefix raw and sort strictly after `after`, in key order.
//
// TWO PREFIXES, AND THE REASON IS NOT TIDINESS. query.Query.Prefix is a
// NAMESPACE: NaiveQueryApply cleans it and appends "/", and go-ds-pebble does
// the same before it computes its iterator bounds, so a partial segment cannot
// be expressed as a query prefix by any mechanism go-datastore offers. An
// object name is exactly one segment, so a list under a partial name is
// exactly that unexpressible thing. The namespace is what narrows the scan
// PHYSICALLY; the raw prefix is what selects LOGICALLY.
//
// WHAT THAT COSTS, said plainly because it is a real cost: a list under a
// narrow prefix walks the whole object namespace of the bucket instance and
// filters. The result is exact and correctly ordered — the brief's §8 promises
// both and both hold — but "no full-bucket scan" does not hold through this
// store, and cannot until something offers a raw lower/upper bound. pebble has
// exactly that natively (pebble.IterOptions LowerBound/UpperBound); what stands
// between is go-ds-pebble's ds.NewKey(q.Prefix)+"/" and pkg/kvdb's set.Elements
// building its own inner query. Neither is this package's to change, so the
// cost is written here rather than assumed away. It is bounded by the BUCKET
// INSTANCE, never by the shard: a scan cannot cross a tenancy boundary, only
// waste work inside one.
func (i index) scan(ctx context.Context, namespace, raw, after string, limit int) ([]row, error) {
	q := query.Query{
		Prefix: namespace,
		Orders: []query.Order{query.OrderByKey{}},
		Limit:  limit,
	}
	if raw != "" {
		q.Filters = append(q.Filters, query.FilterKeyPrefix{Prefix: raw})
	}
	if after != "" {
		q.Filters = append(q.Filters, query.FilterKeyCompare{Op: query.GreaterThan, Key: after})
	}

	res, err := i.store.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer res.Close()

	var out []row
	for {
		e, ok := res.NextSync()
		if !ok {
			return out, nil
		}
		if e.Error != nil {
			return nil, e.Error
		}
		out = append(out, row{key: e.Key, value: e.Value})
	}
}

func (i index) get(ctx context.Context, key string) ([]byte, error) {
	v, err := i.store.Get(ctx, ds.RawKey(key))
	if errors.Is(err, ds.ErrNotFound) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return v, err
}

func (i index) put(ctx context.Context, key string, value []byte) error {
	return i.store.Put(ctx, ds.RawKey(key), value)
}

// Current resolves an object to its live version, or reports it gone.
//
// ONE ROW. The scan is scoped to the object's own namespace — the object name
// is one whole segment, so the store narrows to it physically — asks for key
// order, and takes one entry. A tombstone resolves to ErrNotFound: a delete is
// reported as the object being gone, not as a tombstone the caller has to know
// about.
func (i index) Current(ctx context.Context, a Address, name string) (Version, error) {
	prefix, err := a.objectPrefix(name)
	if err != nil {
		return Version{}, err
	}
	r, ok, err := i.first(ctx, prefix)
	if err != nil {
		return Version{}, err
	}
	if !ok {
		return Version{}, fmt.Errorf("%w: %s in %s", ErrNotFound, name, a)
	}
	v, err := parseVersion(r.key, r.value)
	if err != nil {
		return Version{}, err
	}
	if v.Tombstone() {
		return Version{}, fmt.Errorf("%w: %s in %s is deleted", ErrNotFound, name, a)
	}
	return v, nil
}

// Versions lists an object's versions newest-first, tombstones included —
// which is what the ABI's versions(name) wants, and what a caller repairing a
// mistaken delete needs. A limit of zero means all of them.
func (i index) Versions(ctx context.Context, a Address, name string, limit int) ([]Version, error) {
	prefix, err := a.objectPrefix(name)
	if err != nil {
		return nil, err
	}
	rows, err := i.all(ctx, prefix, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(rows))
	for _, r := range rows {
		v, err := parseVersion(r.key, r.value)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// At resolves one exact stamp. It is the ABI's open(name, version).
//
// A STAMP IS AN ADDRESS, NOT AN IDENTITY, and this is where that bites: two
// nodes writing in the same millisecond with the same logical counter mint the
// same stamp and produce two rows, so this can find more than one. It returns
// them in resolution order rather than picking silently, because a caller that
// asked for an exact version and got one of two without being told has been
// lied to. An opaque string version id in the ABI removes the class; that is a
// host-ABI finding and it is in doc.go.
func (i index) At(ctx context.Context, a Address, name string, stamp uint64) ([]Version, error) {
	prefix, err := a.objectPrefix(name)
	if err != nil {
		return nil, err
	}
	rows, err := i.scan(ctx, prefix, prefix+"/"+stampSegment(stamp)+"/", "", 0)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: %s in %s at stamp %d", ErrNotFound, name, a, stamp)
	}
	out := make([]Version, 0, len(rows))
	for _, r := range rows {
		v, err := parseVersion(r.key, r.value)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return Order(out), nil
}

// Append writes one version row. It is the whole write path at this layer:
// there is no read, no compare and no lock, because the key it computes is a
// key no other writer can compute.
//
// A RETRY IS A NO-OP MERGE. The same (name, stamp, writer) is the same key, so
// a coordinator that resends after a timeout writes the row it already wrote.
func (i index) Append(ctx context.Context, a Address, name string, stamp uint64, writer string, rec VersionRecord) (Version, error) {
	if rec.Writer == "" {
		rec.Writer = writer
	}
	if rec.Writer != writer {
		return Version{}, fmt.Errorf("%w: appending as %q with a record naming %q", ErrWriterMismatch, writer, rec.Writer)
	}
	key, err := a.versionKey(name, stamp, writer)
	if err != nil {
		return Version{}, err
	}
	raw, err := rec.encode()
	if err != nil {
		return Version{}, err
	}
	if err := i.put(ctx, key, raw); err != nil {
		return Version{}, err
	}
	return parseVersion(key, raw)
}
