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

// The node-local rows, never replicated and rebuildable, so deletion is decided
// locally with no distributed refcount:
//
//	/local/blk/<cid>                                    this service's own
//	                                                    block index
//	/local/ref/<rootcid>/<bkt>/<hexname>/<^stamp>       the reference list
//	/local/superseded/<bkt>/<hexname>/<^stamp>          when the version was
//	                                                    first found superseded
//	/local/absent/<bkt>/<hexname>/<^stamp>              when no index holder was
//	                                                    first found holding it
//	/local/adopted/<shard>                              a shard an old holder
//	                                                    handed this node
//
// The blockstore is shared with kvdb's own DAG blocks, so the sweep enumerates only
// /local/blk, written for every imported block. A reference row is written on every
// push that lands here, even when the blocks were already present, or a second
// tenant's reference is lost and the first tenant's delete frees its bytes. Every
// block arrives by a push that records its reference, so the local list is complete.
// An observation is local and first-write-wins, so no stamp can shorten a grace
// window and a restart cannot reset one. They are timed on this node's wall clock.

const (
	localRoot = "/local"

	localBlocks     = localRoot + "/blk"
	localRefs       = localRoot + "/ref"
	localSuperseded = localRoot + "/superseded"
	localAbsent     = localRoot + "/absent"
	localSeen       = localRoot + "/seen"
	localAdopted    = localRoot + "/adopted"
	localClock      = localRoot + "/clock"
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

	// key is the row it was parsed from.
	key string
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
	return Reference{Root: root, Address: addr, Name: name, Stamp: stamp, key: key}, nil
}

// observationKey records when this node first found something of a version: under
// localSuperseded or localAbsent.
func observationKey(namespace string, a Address, name string, stamp uint64) (string, error) {
	inst, err := instanceSegment(a)
	if err != nil {
		return "", err
	}
	nameSeg, err := objectSegment(name)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{namespace, inst, nameSeg, stampSegment(stamp)}, "/"), nil
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

// Saw records that this node was written a row of an instance, so it keeps that
// instance's shard open before the catalog has heard of the bucket.
func (l *Local) Saw(ctx context.Context, a Address) error {
	inst, err := instanceSegment(a)
	if err != nil {
		return err
	}
	return l.store.Put(ctx, ds.RawKey(localSeen+"/"+inst), encodeMS(uint64(l.now().UnixMilli())))
}

// Adopt records that this node holds shard because a holder placement moved it off
// asked whether it covers it.
func (l *Local) Adopt(ctx context.Context, shard uint16) error {
	return l.store.Put(ctx, adoptedKey(shard), encodeMS(uint64(l.now().UnixMilli())))
}

// Disown forgets an adopted shard once it is closed here.
func (l *Local) Disown(ctx context.Context, shard uint16) error {
	return l.store.Delete(ctx, adoptedKey(shard))
}

// Adopted is every shard this node adopted, ordered.
func (l *Local) Adopted(ctx context.Context) ([]uint16, error) {
	keys, err := l.list(ctx, localAdopted)
	if err != nil {
		return nil, err
	}
	out := make([]uint16, 0, len(keys))
	for _, k := range keys {
		n, err := strconv.ParseUint(strings.TrimPrefix(k, localAdopted+"/"), 10, 16)
		if err != nil || n >= Shards {
			return nil, fmt.Errorf("%w: adopted row %q", ErrNotThisServicesRow, k)
		}
		out = append(out, uint16(n))
	}
	return out, nil
}

// adoptedKey pads the shard, so the rows list in shard order.
func adoptedKey(shard uint16) ds.Key {
	return ds.RawKey(fmt.Sprintf("%s/%03d", localAdopted, shard))
}

// ClockFloor is the physical millisecond this node's clock may never mint below: 0
// for a node that never minted.
func (l *Local) ClockFloor(ctx context.Context) (uint64, error) {
	raw, err := l.store.Get(ctx, ds.RawKey(localClock))
	if errors.Is(err, ds.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return decodeMS(raw)
}

// SetClockFloor raises the floor to ms, durably before it returns: a stamp minted
// below a floor a crash took would be one the next start mints below again.
func (l *Local) SetClockFloor(ctx context.Context, ms uint64) error {
	if err := l.store.Put(ctx, ds.RawKey(localClock), encodeMS(ms)); err != nil {
		return err
	}
	return l.store.Sync(ctx, ds.RawKey(localClock))
}

// Seen is every instance this node was written a row of.
func (l *Local) Seen(ctx context.Context) ([]Address, error) {
	keys, err := l.list(ctx, localSeen)
	if err != nil {
		return nil, err
	}
	out := make([]Address, 0, len(keys))
	for _, k := range keys {
		a, err := addressFromInstanceSegment(strings.TrimPrefix(k, localSeen+"/"))
		if err != nil {
			return nil, fmt.Errorf("%w: seen row %q: %w", ErrNotThisServicesRow, k, err)
		}
		out = append(out, a)
	}
	return out, nil
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

// Reference records that a bucket instance's object version uses a root, on this
// node, even when every block was already present (see the file header).
func (l *Local) Reference(ctx context.Context, root cid.Cid, a Address, name string, stamp uint64) error {
	key, err := refKey(root, a, name, stamp)
	if err != nil {
		return err
	}
	return l.store.Put(ctx, ds.RawKey(key), encodeMS(uint64(l.now().UnixMilli())))
}

// Sync makes this node's rows durable.
func (l *Local) Sync(ctx context.Context) error {
	return l.store.Sync(ctx, ds.NewKey("/"))
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

// Superseded records the local instant the version was first found superseded and
// returns the instant on record. FIRST OBSERVATION WINS: finding it again neither
// advances nor resets it.
func (l *Local) Superseded(ctx context.Context, a Address, name string, stamp uint64) (time.Time, error) {
	return l.observe(ctx, localSuperseded, a, name, stamp)
}

// Absent records the local instant no index holder was first found holding the
// version's row, first observation winning; Present forgets it.
func (l *Local) Absent(ctx context.Context, a Address, name string, stamp uint64) (time.Time, error) {
	return l.observe(ctx, localAbsent, a, name, stamp)
}

// Present forgets that the version's row was found absent: a holder holds it.
func (l *Local) Present(ctx context.Context, a Address, name string, stamp uint64) error {
	key, err := observationKey(localAbsent, a, name, stamp)
	if err != nil {
		return err
	}
	return l.store.Delete(ctx, ds.RawKey(key))
}

// Observed is what this node found of a version so far: when it was first found
// superseded and first found absent, the zero time for never.
func (l *Local) Observed(ctx context.Context, a Address, name string, stamp uint64) (superseded, absent time.Time, err error) {
	if superseded, err = l.observed(ctx, localSuperseded, a, name, stamp); err != nil {
		return time.Time{}, time.Time{}, err
	}
	absent, err = l.observed(ctx, localAbsent, a, name, stamp)
	return superseded, absent, err
}

// Collected forgets what was found of a version once its reference is dropped:
// keeping it would leak a row per collected version.
func (l *Local) Collected(ctx context.Context, a Address, name string, stamp uint64) error {
	for _, namespace := range []string{localSuperseded, localAbsent} {
		key, err := observationKey(namespace, a, name, stamp)
		if err != nil {
			return err
		}
		if err := l.store.Delete(ctx, ds.RawKey(key)); err != nil {
			return err
		}
	}
	return nil
}

func (l *Local) observe(ctx context.Context, namespace string, a Address, name string, stamp uint64) (time.Time, error) {
	at, err := l.observed(ctx, namespace, a, name, stamp)
	if err != nil || !at.IsZero() {
		return at, err
	}
	key, err := observationKey(namespace, a, name, stamp)
	if err != nil {
		return time.Time{}, err
	}
	now := l.now()
	if err := l.store.Put(ctx, ds.RawKey(key), encodeMS(uint64(now.UnixMilli()))); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

func (l *Local) observed(ctx context.Context, namespace string, a Address, name string, stamp uint64) (time.Time, error) {
	key, err := observationKey(namespace, a, name, stamp)
	if err != nil {
		return time.Time{}, err
	}
	raw, err := l.store.Get(ctx, ds.RawKey(key))
	if errors.Is(err, ds.ErrNotFound) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	ms, err := decodeMS(raw)
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(int64(ms)), nil
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

// encodeMS and decodeMS are the value codec for the row kinds above: one
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
