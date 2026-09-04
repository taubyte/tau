package storage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

// refs.go is the node-local half of the layout: four structures that are never
// replicated, are rebuildable from the buckets this node holds, and exist so
// that A DELETION DECISION IS MADE LOCALLY WITH NO DISTRIBUTED REFCOUNT.
//
//	/local/blk/<cid>                                    this service's OWN
//	                                                    block index
//	/local/ref/<rootcid>/<bkt>/<hexname>/<^stamp>       the reference list
//	/local/tomb/<bkt>/<hexname>/<^stamp>                when a tombstone was
//	                                                    first OBSERVED here
//	/local/dirty/<bkt>                                  the dirty counter
//
// ── THE BLOCK INDEX IS THE CORRECTNESS LANDMINE ──────────────────────────
//
// THE NODE'S BLOCKSTORE IS NOT THIS SERVICE'S. It is shared with pkg/kvdb's
// own Merkle-DAG blocks — every delta of every replicated store on this node —
// and with every other consumer of the same peer. A sweep that enumerated
// blockstore.AllKeysChan and deleted what it could not account for would delete
// the CRDT's own history.
//
// So this service writes /local/blk/<cid> for EVERY block it imports, and the
// sweep enumerates THAT and nothing else. It is the reason config.go's Blocks
// interface has no AllKeysChan: the door is not narrow by taste, it is narrow
// because the wide version is a data-destruction bug.
//
// ── THE REFERENCE LIST IS WRITTEN EVEN WHEN THE BLOCK WAS ALREADY HERE ───
//
// This is the dedup hole, and it is easy to miss. A second tenant pushes bytes
// this node already holds; the import finds the DAG present and does nothing;
// the holder is now ignorant of the second reference, and deleting the first
// tenant's object frees blocks the second one is still using.
//
// So a reference row is written on EVERY push that lands here, present or not.
// The row is keyed by (root, bucket instance, object, stamp), so a re-push of
// the same version writes the same row and two tenants pushing identical bytes
// write two.
//
// ── WHY NO DISTRIBUTED REFCOUNT IS NEEDED ────────────────────────────────
//
// A block reaches a node only because some push put it there, and every push
// records its reference locally. So THE NODE'S REFERENCE LIST IS COMPLETE FOR
// THAT NODE, and completeness is what a refcount would have bought. A refcount
// would additionally be a contended counter replicated between nodes, which is
// the one shape AGENTS.md §1 forbids outright.
//
// ── THE TOMBSTONE OBSERVATION IS LOCAL AND MONOTONIC ─────────────────────
//
// The grace window before blocks are released is timed from WHEN THIS NODE
// FIRST SAW THE TOMBSTONE, recorded here, and never from the stamp's physical
// component. That is what keeps every clock out of the path that destroys
// data: a peer with a clock a year fast cannot shorten anybody's grace window,
// because nothing on that path reads its clock.
//
// FIRST OBSERVATION WINS. Re-observing does not move the row forward, so a
// process restart that re-scans a shard cannot reset a grace window that was
// nearly elapsed, and cannot extend one either.

const (
	localRoot = "/local"

	localBlocks = localRoot + "/blk"
	localRefs   = localRoot + "/ref"
	localTombs  = localRoot + "/tomb"
	localDirty  = localRoot + "/dirty"
)

// instanceSegment encodes a whole bucket instance into ONE path segment, so a
// reference row's depth does not depend on any identity's content.
//
// The four parts are joined with ".", which base64url's alphabet
// (A-Za-z0-9-_) cannot produce — so the join is unambiguous and reversible,
// and the result contains no "/" and is not "." or "..". It is one segment for
// the same reason auth's identities are: a boundary that can move is a
// namespace another tenant can reach into.
func instanceSegment(a Address) (string, error) {
	if !a.Valid() {
		return "", ErrNoAddress
	}
	b := a.Bucket()
	return strings.Join([]string{
		segment(b.Project()), segment(b.Environment()), segment(b.Name()), segment(a.Instance()),
	}, "."), nil
}

// addressFromInstanceSegment reverses instanceSegment.
func addressFromInstanceSegment(seg string) (Address, error) {
	parts := strings.Split(seg, ".")
	if len(parts) != 4 {
		return Address{}, fmt.Errorf("%w: instance segment %q has %d parts, not 4", ErrNotThisServicesRow, seg, len(parts))
	}
	out := make([]string, 4)
	for i, p := range parts {
		v, err := unsegment(p)
		if err != nil {
			return Address{}, err
		}
		out[i] = v
	}
	return NewAddress(out[0], out[1], out[2], out[3])
}

// cidSegment is a CID as a path segment. A CID's string form is multibase, so
// it holds no "/" — but this checks rather than assumes, because a key built
// from an unchecked value is the whole class store.go exists to close.
func cidSegment(c cid.Cid) (string, error) {
	if !c.Defined() {
		return "", fmt.Errorf("%w: an undefined cid", ErrNotThisServicesRow)
	}
	s := c.String()
	if s == "" || strings.ContainsAny(s, "/") || s == "." || s == ".." {
		return "", fmt.Errorf("%w: cid %q is not one path segment", ErrNotThisServicesRow, s)
	}
	return s, nil
}

// Local is this node's own rows. It is a separate store from any shard: these
// rows describe what THIS node holds and are meaningless anywhere else, so
// replicating them would be replicating a lie.
type Local struct {
	store Datastore
	now   func() time.Time
}

// NewLocal builds the node-local row set.
func NewLocal(store Datastore, now func() time.Time) (*Local, error) {
	if store == nil {
		return nil, errors.New("storage: no local store")
	}
	if now == nil {
		now = time.Now
	}
	return &Local{store: store, now: now}, nil
}

// blockKey is a row in this service's own block index.
func blockKey(c cid.Cid) (string, error) {
	seg, err := cidSegment(c)
	if err != nil {
		return "", err
	}
	return localBlocks + "/" + seg, nil
}

func cidFromBlockKey(key string) (cid.Cid, error) {
	rest, ok := strings.CutPrefix(key, localBlocks+"/")
	if !ok || strings.Contains(rest, "/") {
		return cid.Undef, fmt.Errorf("%w: %q is not a block index row", ErrNotThisServicesRow, key)
	}
	c, err := cid.Decode(rest)
	if err != nil {
		return cid.Undef, fmt.Errorf("%w: block index row %q: %w", ErrNotThisServicesRow, key, err)
	}
	return c, nil
}

// refKey is a reference row: this bucket instance's object, at this version,
// uses this root.
func refKey(root cid.Cid, a Address, name string, stamp uint64) (string, error) {
	rootSeg, err := cidSegment(root)
	if err != nil {
		return "", err
	}
	inst, err := instanceSegment(a)
	if err != nil {
		return "", err
	}
	nameSeg, err := objectSegment(name)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{localRefs, rootSeg, inst, nameSeg, stampSegment(stamp)}, "/"), nil
}

// Reference is a parsed reference row.
type Reference struct {
	Root    cid.Cid
	Address Address
	Name    string
	Stamp   uint64
}

func parseRefKey(key string) (Reference, error) {
	rest, ok := strings.CutPrefix(key, localRefs+"/")
	if !ok {
		return Reference{}, fmt.Errorf("%w: %q is not a reference row", ErrNotThisServicesRow, key)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 4 {
		return Reference{}, fmt.Errorf("%w: reference row %q has %d parts, not 4", ErrNotThisServicesRow, key, len(parts))
	}
	root, err := cid.Decode(parts[0])
	if err != nil {
		return Reference{}, fmt.Errorf("%w: reference row %q: %w", ErrNotThisServicesRow, key, err)
	}
	addr, err := addressFromInstanceSegment(parts[1])
	if err != nil {
		return Reference{}, err
	}
	name, err := objectName(parts[2])
	if err != nil {
		return Reference{}, err
	}
	stamp, err := stampFromSegment(parts[3])
	if err != nil {
		return Reference{}, err
	}
	return Reference{Root: root, Address: addr, Name: name, Stamp: stamp}, nil
}

// tombKey records when this node first observed a tombstone.
func tombKey(a Address, name string, stamp uint64) (string, error) {
	inst, err := instanceSegment(a)
	if err != nil {
		return "", err
	}
	nameSeg, err := objectSegment(name)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{localTombs, inst, nameSeg, stampSegment(stamp)}, "/"), nil
}

// dirtyKey is the per-instance deletion counter: an instance with no deletions
// since the last sweep is skipped entirely.
func dirtyKey(a Address) (string, error) {
	inst, err := instanceSegment(a)
	if err != nil {
		return "", err
	}
	return localDirty + "/" + inst, nil
}

// Imported records that this node imported a block. It is written for every
// block, and it is the ONLY enumeration the sweep is allowed to make.
func (l *Local) Imported(ctx context.Context, c cid.Cid) error {
	key, err := blockKey(c)
	if err != nil {
		return err
	}
	return l.store.Put(ctx, ds.RawKey(key), encodeMS(uint64(l.now().UnixMilli())))
}

// Forget removes a block's index row. The sweep calls it AFTER the block is
// gone — see gc.go for why that order and not the other.
func (l *Local) Forget(ctx context.Context, c cid.Cid) error {
	key, err := blockKey(c)
	if err != nil {
		return err
	}
	return l.store.Delete(ctx, ds.RawKey(key))
}

// IndexedBlocks lists every block this service imported on this node.
func (l *Local) IndexedBlocks(ctx context.Context) ([]cid.Cid, error) {
	keys, err := l.list(ctx, localBlocks)
	if err != nil {
		return nil, err
	}
	out := make([]cid.Cid, 0, len(keys))
	for _, k := range keys {
		c, err := cidFromBlockKey(k)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// Reference records that a bucket instance's object version uses a root, on
// this node. IT IS WRITTEN EVEN WHEN EVERY BLOCK WAS ALREADY PRESENT — see the
// file header: that is the dedup hole, and this is what closes it.
func (l *Local) Reference(ctx context.Context, root cid.Cid, a Address, name string, stamp uint64) error {
	key, err := refKey(root, a, name, stamp)
	if err != nil {
		return err
	}
	return l.store.Put(ctx, ds.RawKey(key), encodeMS(uint64(l.now().UnixMilli())))
}

// Dereference drops one reference row. It is the second half of a delete on
// this node, and it never touches a block: blocks are freed by the sweep,
// behind the grace window, once nothing references them.
func (l *Local) Dereference(ctx context.Context, root cid.Cid, a Address, name string, stamp uint64) error {
	key, err := refKey(root, a, name, stamp)
	if err != nil {
		return err
	}
	return l.store.Delete(ctx, ds.RawKey(key))
}

// References lists every reference this node holds, parsed.
func (l *Local) References(ctx context.Context) ([]Reference, error) {
	keys, err := l.list(ctx, localRefs)
	if err != nil {
		return nil, err
	}
	out := make([]Reference, 0, len(keys))
	for _, k := range keys {
		r, err := parseRefKey(k)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// ReferencesTo lists this node's references to one root — the question
// "may I free these bytes" reduces to.
func (l *Local) ReferencesTo(ctx context.Context, root cid.Cid) ([]Reference, error) {
	seg, err := cidSegment(root)
	if err != nil {
		return nil, err
	}
	keys, err := l.list(ctx, localRefs+"/"+seg)
	if err != nil {
		return nil, err
	}
	out := make([]Reference, 0, len(keys))
	for _, k := range keys {
		r, err := parseRefKey(k)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// ObservedTombstone records the local instant a tombstone was first seen here,
// and returns the instant on record. FIRST OBSERVATION WINS: re-observing
// neither advances nor resets it.
func (l *Local) ObservedTombstone(ctx context.Context, a Address, name string, stamp uint64) (time.Time, error) {
	key, err := tombKey(a, name, stamp)
	if err != nil {
		return time.Time{}, err
	}
	raw, err := l.store.Get(ctx, ds.RawKey(key))
	switch {
	case err == nil:
		ms, derr := decodeMS(raw)
		if derr != nil {
			return time.Time{}, derr
		}
		return time.UnixMilli(int64(ms)), nil
	case errors.Is(err, ds.ErrNotFound):
	default:
		return time.Time{}, err
	}

	now := l.now()
	if err := l.store.Put(ctx, ds.RawKey(key), encodeMS(uint64(now.UnixMilli()))); err != nil {
		return time.Time{}, err
	}
	if err := l.MarkDirty(ctx, a); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

// ForgetTombstone drops an observation row once the version it describes has
// been swept. Keeping it would leak a row per deleted object forever.
func (l *Local) ForgetTombstone(ctx context.Context, a Address, name string, stamp uint64) error {
	key, err := tombKey(a, name, stamp)
	if err != nil {
		return err
	}
	return l.store.Delete(ctx, ds.RawKey(key))
}

// MarkDirty bumps a bucket instance's deletion counter. It is node-local, so
// it has exactly one writer and a read-modify-write on it is legal — the one
// condition AGENTS.md §1 attaches to a counter.
func (l *Local) MarkDirty(ctx context.Context, a Address) error {
	key, err := dirtyKey(a)
	if err != nil {
		return err
	}
	n, err := l.dirty(ctx, key)
	if err != nil {
		return err
	}
	return l.store.Put(ctx, ds.RawKey(key), encodeMS(n+1))
}

// Dirty reports a bucket instance's deletion count since the last sweep. An
// instance reading zero is skipped by the sweep entirely, which is what keeps
// a sweep proportional to what changed rather than to what exists.
func (l *Local) Dirty(ctx context.Context, a Address) (uint64, error) {
	key, err := dirtyKey(a)
	if err != nil {
		return 0, err
	}
	return l.dirty(ctx, key)
}

// Swept clears a bucket instance's deletion counter.
func (l *Local) Swept(ctx context.Context, a Address) error {
	key, err := dirtyKey(a)
	if err != nil {
		return err
	}
	return l.store.Delete(ctx, ds.RawKey(key))
}

// DirtyInstances lists the bucket instances with deletions to sweep.
func (l *Local) DirtyInstances(ctx context.Context) ([]Address, error) {
	keys, err := l.list(ctx, localDirty)
	if err != nil {
		return nil, err
	}
	out := make([]Address, 0, len(keys))
	for _, k := range keys {
		rest, ok := strings.CutPrefix(k, localDirty+"/")
		if !ok || strings.Contains(rest, "/") {
			return nil, fmt.Errorf("%w: %q is not a dirty row", ErrNotThisServicesRow, k)
		}
		a, err := addressFromInstanceSegment(rest)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (l *Local) dirty(ctx context.Context, key string) (uint64, error) {
	raw, err := l.store.Get(ctx, ds.RawKey(key))
	if errors.Is(err, ds.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return decodeMS(raw)
}

// list returns every key under a namespace, in key order.
func (l *Local) list(ctx context.Context, namespace string) ([]string, error) {
	res, err := l.store.Query(ctx, query.Query{
		Prefix:   namespace,
		Orders:   []query.Order{query.OrderByKey{}},
		KeysOnly: true,
	})
	if err != nil {
		return nil, err
	}
	defer res.Close()

	var out []string
	for {
		e, ok := res.NextSync()
		if !ok {
			return out, nil
		}
		if e.Error != nil {
			return nil, e.Error
		}
		out = append(out, e.Key)
	}
}

// encodeMS and decodeMS are the value codec for the four row kinds above: one
// unsigned integer, in decimal, so a row read by eye during an incident says
// what it is. None of them is ordered by its VALUE — every ordering in this
// service is in a key — so there is no zero-padding requirement here.
func encodeMS(v uint64) []byte { return []byte(strconv.FormatUint(v, 10)) }

func decodeMS(raw []byte) (uint64, error) {
	v, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q: %w", ErrNotThisServicesRow, raw, err)
	}
	return v, nil
}
