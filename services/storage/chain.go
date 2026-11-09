package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"

	"github.com/taubyte/tau/pkg/proto/codec"
	storagev2 "github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2"
)

// There is no stored pointer. "The current version of object X" is a derived
// maximum over an append-only, per-writer-disjoint version list:
//
//	/b/<proj>/<env>/<bkt>/<inst>/o/<hexname>/<^stamp>/<writer> -> VersionRecord
//
//	THE WRITER IS IN THE KEY      so two nodes cannot write one key with
//	                              different content.
//	THE STAMP IS COMPLEMENTED     so byte order is newest-first and "current"
//	                              is one row, not a sort over a chain.
//	THE VALUE CARRIES EVERYTHING  so resolution costs one row and never a
//	  head NEEDS                  block fetch.
//
// Resolution: scan the object's prefix, take the lowest key; a tombstone or no row
// means gone. No coordination and no clock comparison, only byte order over
// converged state.
//
// The CID is not the version, since CIDs have no "later". kvdb's own merge rule is
// not used, since on a tie it silently discards one write. A delete is a tombstone
// row acked like a put, never a kvdb.Delete, so a delete racing a re-create cannot
// lose.
//
// The key's byte order and Newest's comparison must be one order: Newest compares
// the encoded writer segment, since base64url is not order-preserving.
// TestTheKeyOrderAndTheResolutionOrderAreOneOrder holds this.

// VersionRecord is the value of a version row: everything head needs, so resolution
// never fetches a block. Stored as storagev2.VersionRecord; it never crosses a wire.
type VersionRecord struct {
	// RootCID is the UnixFS root of the object's bytes. It is an ADDRESS, not
	// a credential — a caller is authorized on the bucket, never on knowing
	// this. It never crosses the RPC boundary to a guest.
	RootCID string

	// Size is the object's byte length, carried so head and the usage sum
	// never touch a block.
	Size uint64

	ContentType string

	// CreatedMS is wall-clock milliseconds, for display only: ordering is the stamp and
	// the grace window is timed from local observation.
	CreatedMS uint64

	// Writer names the node that wrote this row, duplicating the key's last segment. The
	// key is authoritative; a disagreeing record is caught on decode.
	Writer string

	// Tombstone marks this version as a delete. The row is a row like any
	// other: it has a stamp, it is acked on K, and a re-create with a later
	// stamp wins over it visibly.
	Tombstone bool

	// Exclusive records that every block of this object was new to this bucket
	// instance when it was imported. It is the GC fast path: deleting an
	// exclusive object frees its DAG with no mark phase at all. It is only
	// ever a HINT THAT SAVES WORK — false means "walk", never "keep".
	Exclusive bool

	// Metadata is the caller's own name/value pairs, carried so head answers
	// them without the bytes.
	Metadata map[string]string

	// Kept is a version its bucket keeps once a later one supersedes it, so its
	// holders never collect it (collect.go).
	Kept bool

	// ExpiresMS is the wall millisecond the version is gone at, by its bucket's
	// lifecycle.expire, current or not and kept or not; zero is never. Timed on
	// the writer's clock, it is a floor, not a guarantee.
	ExpiresMS uint64
}

// expired is a version gone by at; the zero time resolves none.
func (r VersionRecord) expired(at time.Time) bool {
	return r.ExpiresMS != 0 && !at.IsZero() && uint64(at.UnixMilli()) >= r.ExpiresMS
}

func (r VersionRecord) encode() ([]byte, error) {
	return wireRecord(r).MarshalVT()
}

// decodeVersionRecord refuses an empty row: every record written carries a root or a
// tombstone.
func decodeVersionRecord(raw []byte) (VersionRecord, error) {
	var m storagev2.VersionRecord
	if err := codec.DecodeNonEmpty(raw, &m); err != nil {
		return VersionRecord{}, fmt.Errorf("%w: %w", ErrNotThisServicesRow, err)
	}
	return recordOf(&m), nil
}

// Version is one row, key and value together, derived entirely from the row.
type Version struct {
	VersionKey
	Record VersionRecord
}

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

// Newest is the resolution rule as a pure function: greatest stamp wins, ties broken
// by the encoded writer segment ascending, which is what byte order over the keys
// does. Separate from the scan so it can be tested without a store.
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
type index struct {
	store Datastore

	// meter is the emission seam (meter.go). Nil is the ordinary case.
	meter Meter

	// at is the instant reads resolve expiry at: a version past its bucket's
	// lifecycle.expire is gone. The zero time resolves none.
	at time.Time
}

// row is one scanned key and value.
type row struct {
	key   string
	value []byte
}

// first returns the lowest-keyed row under a whole-segment namespace. It asks for key
// order explicitly: go-datastore promises none, and ds.MapDatastore returns rows in
// arbitrary order. OrderByKey costs pebble nothing.
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

// scan returns rows under a whole-segment namespace whose keys also begin with the
// raw byte prefix raw and sort strictly after `after`, in key order.
//
// Two prefixes because go-datastore's query prefix is a namespace (cleaned, with "/"
// appended), so a partial object name cannot be a query prefix. The namespace narrows
// physically, the raw prefix selects logically. So a narrow-prefix list walks the
// whole object namespace of the bucket instance and filters: exact and ordered, but
// not cheap, until something exposes pebble's raw iterator bounds. It is bounded by
// the bucket instance and never crosses a tenancy boundary.
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

// holdsAny says this copy holds a row of an object of the instance.
func (i index) holdsAny(ctx context.Context, a Address) (bool, error) {
	prefix, err := a.objectsPrefix()
	if err != nil {
		return false, err
	}
	_, ok, err := i.first(ctx, prefix)
	return ok, err
}

// Current resolves an object to its live version: one row from the object's own
// namespace in key order. A tombstone resolves to ErrNotFound.
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
	if v.Record.expired(i.at) {
		return Version{}, fmt.Errorf("%w: %s in %s expired", ErrNotFound, name, a)
	}
	return v, nil
}

// Versions lists an object's versions newest-first, tombstones included and expired
// ones not. A limit of zero means all.
func (i index) Versions(ctx context.Context, a Address, name string, limit int) ([]Version, error) {
	prefix, err := a.objectPrefix(name)
	if err != nil {
		return nil, err
	}
	rows, err := i.all(ctx, prefix, 0)
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(rows))
	for _, r := range rows {
		v, err := parseVersion(r.key, r.value)
		if err != nil {
			return nil, err
		}
		if v.Record.expired(i.at) {
			continue
		}
		out = append(out, v)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

// List is the current version of every live object whose name begins with prefix
// and sorts after after, in name order. An object's rows are contiguous and
// newest-first, so its first row is its current version and a tombstone there
// leaves it out.
func (i index) List(ctx context.Context, a Address, prefix, after string, limit int) ([]Version, error) {
	namespace, err := a.objectsPrefix()
	if err != nil {
		return nil, err
	}
	raw, err := a.listPrefix(prefix)
	if err != nil {
		return nil, err
	}
	var past string
	if after != "" {
		object, err := a.objectPrefix(after)
		if err != nil {
			return nil, err
		}
		// Past every row of after: its stamps and writers sort below '~', and a
		// name extending after sorts above this.
		past = object + "/~"
	}
	rows, err := i.scan(ctx, namespace, raw, past, 0)
	if err != nil {
		return nil, err
	}

	var (
		out  []Version
		last string
		seen bool
	)
	for _, r := range rows {
		v, err := parseVersion(r.key, r.value)
		if err != nil {
			return nil, err
		}
		if seen && v.Name == last {
			continue
		}
		seen, last = true, v.Name
		if v.Tombstone() || v.Record.expired(i.at) {
			continue
		}
		out = append(out, v)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

// At resolves one exact stamp (the ABI's open(name, version)). Two nodes can mint the
// same stamp in one millisecond, so it may find more than one and returns them all in
// resolution order rather than picking silently.
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

// standing is what one copy of an index holds of a version: its row, a newer row of
// its name, whether the row says its bucket keeps it, and whether it expired.
type standing struct {
	held       bool
	superseded bool
	kept       bool
	expired    bool
}

// Standing is what this copy holds of the version of name at stamp whose bytes are
// root. A copy without that row holds nothing of it, whatever else it holds.
func (i index) Standing(ctx context.Context, a Address, name string, stamp uint64, root string) (standing, error) {
	prefix, err := a.objectPrefix(name)
	if err != nil {
		return standing{}, err
	}
	rows, err := i.all(ctx, prefix+"/"+stampSegment(stamp), 0)
	if err != nil {
		return standing{}, err
	}
	var at Version
	for _, r := range rows {
		v, err := parseVersion(r.key, r.value)
		if err != nil {
			return standing{}, err
		}
		if v.Record.RootCID == root {
			at = v
			break
		}
	}
	if at.Record.RootCID == "" {
		return standing{}, nil
	}
	newest, ok, err := i.first(ctx, prefix)
	if err != nil || !ok {
		return standing{}, err
	}
	n, err := parseVersion(newest.key, newest.value)
	if err != nil {
		return standing{}, err
	}
	return standing{
		held: true, kept: at.Record.Kept, expired: at.Record.expired(i.at),
		superseded: n.Stamp != at.Stamp || n.Writer != at.Writer,
	}, nil
}
