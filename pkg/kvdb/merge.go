package kvdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	dshelp "github.com/ipfs/boxo/datastore/dshelp"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// Every merge is the join of the add-wins observed-remove set with a causal
// context (Almeida, Shoker, Baquero, "Delta State Replicated Data Types"):
//
//	L = (L1 ∩ L2) ∪ (L1 \ ctx2) ∪ (L2 \ ctx1),  ctx = ctx1 ⊔ ctx2
//
// where an element is a (key, dot) pair and ctx covers a pair when it covers
// the dot or holds a pending removal of that pair. A plain delta is the state
// (its elements, {its dot}, its tombstones); a snapshot is the state of the
// replica that compacted. The join is associative, commutative and
// idempotent, so every replica that has merged the same deltas, in any order
// and any number of times, holds the same state.
//
// It holds because three things hold: a dot is never reused (see dot), a
// context covers an author's chain only up to a prefix it has merged (see
// causalContext), and a state's elements are all covered by its own context.

type blockKind byte

const (
	kindDelta blockKind = iota
	kindSnapshot
	kindChunk
)

// A link is a parent block a delta names, with its dot.
type link struct {
	cid cid.Cid
	dot dot
}

func (store *Datastore) blocksPrefix() ds.Key {
	return store.namespace.ChildString(processedBlocksNs)
}

func (store *Datastore) blockKey(c cid.Cid) ds.Key {
	return store.blocksPrefix().Child(dshelp.MultihashToDsKey(c.Hash()))
}

func (store *Datastore) missingPrefix() ds.Key {
	return store.namespace.ChildString(missingNs)
}

func (store *Datastore) missingKey(d dot) ds.Key {
	return store.missingPrefix().ChildString(d.segment())
}

func (store *Datastore) vectorKey(replica string) ds.Key {
	return store.namespace.ChildString(contextNs).ChildString("v").ChildString(replicaSegment(replica))
}

func (store *Datastore) cloudKey(d dot) ds.Key {
	return store.namespace.ChildString(contextNs).ChildString("d").ChildString(d.segment())
}

func cidOfBlockKey(prefix ds.Key, k string) (cid.Cid, error) {
	return dshelp.DsKeyToCidV1(ds.NewKey(strings.TrimPrefix(k, prefix.String())), cid.DagProtobuf)
}

func encodeBlock(kind blockKind, d dot) []byte {
	return append([]byte{byte(kind)}, encodeDot(d)...)
}

func decodeBlock(v []byte) (blockKind, dot, error) {
	if len(v) < 2 {
		return 0, dot{}, errors.New("error decoding block entry")
	}
	d, err := decodeDot(v[1:])
	return blockKind(v[0]), d, err
}

func (store *Datastore) loadContext(ctx context.Context) (*causalContext, error) {
	c := newCausalContext()
	vp := store.namespace.ChildString(contextNs).ChildString("v")
	err := scanStore(ctx, store.store, vp, func(k string, v []byte) error {
		seg := strings.TrimPrefix(k, vp.String()+"/")
		replica, err := segEncoding.DecodeString(strings.TrimPrefix(seg, "r"))
		if err != nil {
			return err
		}
		counter, n := binary.Uvarint(v)
		if n <= 0 {
			return errors.New("error decoding vector entry")
		}
		c.vector[string(replica)] = counter
		return nil
	})
	if err != nil {
		return nil, err
	}
	dp := store.namespace.ChildString(contextNs).ChildString("d")
	err = scanStore(ctx, store.store, dp, func(k string, v []byte) error {
		d, err := parseDotSegment(strings.TrimPrefix(k, dp.String()+"/"))
		if err != nil {
			return err
		}
		prev, n := binary.Uvarint(v)
		if n <= 0 {
			return errors.New("error decoding cloud entry")
		}
		if c.cloud[d.replica] == nil {
			c.cloud[d.replica] = map[uint64]uint64{}
		}
		c.cloud[d.replica][d.counter] = prev
		return nil
	})
	return c, err
}

// stageContext writes the difference between two contexts.
func (store *Datastore) stageContext(t *txn, old, cur *causalContext) {
	for r, v := range cur.vector {
		if old.vector[r] != v {
			t.put(store.vectorKey(r), binary.AppendUvarint(nil, v))
		}
	}
	for r, cl := range old.cloud {
		for k := range cl {
			if _, ok := cur.cloud[r][k]; !ok {
				t.del(store.cloudKey(dot{replica: r, counter: k}))
			}
		}
	}
	for r, cl := range cur.cloud {
		for k, p := range cl {
			if _, ok := old.cloud[r][k]; !ok {
				t.put(store.cloudKey(dot{replica: r, counter: k}), binary.AppendUvarint(nil, p))
			}
		}
	}
}

// merging is one merge in progress: its writes, its next context, its head
// edits and what it did to each key.
type merging struct {
	store   *Datastore
	t       *txn
	cc      *causalContext
	heads   *headChanges
	touched map[string]*change
	live    int
	blocks  int
}

func (store *Datastore) beginMerge() *merging {
	return &merging{
		store:   store,
		t:       newTxn(store.store),
		cc:      store.cc.clone(),
		heads:   newHeadChanges(),
		touched: map[string]*change{},
	}
}

func (m *merging) touch(key string) *change {
	ch := m.touched[key]
	if ch == nil {
		ch = &change{}
		m.touched[key] = ch
	}
	return ch
}

func (m *merging) hasElem(ctx context.Context, key string, d dot) (bool, error) {
	return m.t.has(ctx, m.store.set.elemKey(key, d))
}

func (m *merging) addElem(ctx context.Context, e element) {
	m.t.put(m.store.set.elemKey(e.key, e.dot), encodeMarker(e.priority, e.value))
	m.touch(e.key)
	m.live++
}

func (m *merging) removeElem(key string, d dot) {
	m.t.del(m.store.set.elemKey(key, d))
	m.touch(key).removed = true
	m.live--
}

// remove applies a tombstone for (key, target): it takes the element out if
// it is here, forgets it if the context already covers its dot, and otherwise
// keeps it pending so the element is refused when it arrives.
func (m *merging) remove(ctx context.Context, key string, target dot) error {
	here, err := m.hasElem(ctx, key, target)
	if err != nil {
		return err
	}
	switch {
	case here:
		m.removeElem(key, target)
	case m.cc.covers(target):
	default:
		m.t.put(m.store.set.pendingKey(key, target), nil)
	}
	return nil
}

// admit adds an element unless the context says it was removed.
func (m *merging) admit(ctx context.Context, e element, local *causalContext) error {
	here, err := m.hasElem(ctx, e.key, e.dot)
	if err != nil || here || local.covers(e.dot) {
		return err
	}
	pk := m.store.set.pendingKey(e.key, e.dot)
	pending, err := m.t.has(ctx, pk)
	if err != nil {
		return err
	}
	if pending {
		m.t.del(pk)
		return nil
	}
	m.addElem(ctx, e)
	return nil
}

// link records a merged block's parents: a covered parent is no longer a
// head, an uncovered one is missing and is returned to be walked.
func (m *merging) link(ctx context.Context, parents []*pb.Parent) ([]link, error) {
	var walk []link
	for _, p := range parents {
		c, err := cid.Cast(p.GetCid())
		if err != nil {
			return nil, fmt.Errorf("bad parent cid: %w", err)
		}
		d := dotFromPB(p.GetDot())
		if _, isHead := m.store.heads.Get(c); isHead {
			m.heads.removeHead(c)
		}
		if m.cc.covers(d) {
			continue
		}
		m.t.put(m.store.missingKey(d), c.Bytes())
		walk = append(walk, link{cid: c, dot: d})
	}
	return walk, nil
}

// linked reports whether a merged block already links d. It is read before
// the merge changes the context, which drops missing entries it covers.
func (m *merging) linked(ctx context.Context, d dot) (bool, error) {
	return m.t.has(ctx, m.store.missingKey(d))
}

// settleBlock makes the merged block a head unless a merged block already
// links it, and indexes it for purging.
func (m *merging) settleBlock(c cid.Cid, d dot, height uint64, kind blockKind, linked bool) {
	m.t.del(m.store.missingKey(d))
	if !linked {
		m.heads.addHead(Head{Cid: c, Height: height, Dot: d})
	}
	m.indexBlock(c, kind, d)
}

func (m *merging) indexBlock(c cid.Cid, kind blockKind, d dot) {
	m.t.put(m.store.blockKey(c), encodeBlock(kind, d))
	m.blocks++
}

func (m *merging) commit(ctx context.Context) error {
	if err := m.store.set.settle(ctx, m.t, m.touched); err != nil {
		return err
	}
	m.store.stageContext(m.t, m.store.cc, m.cc)
	m.store.heads.stage(m.t, m.heads)
	if err := m.t.commit(ctx); err != nil {
		return err
	}
	m.store.setContext(m.cc)
	m.store.heads.apply(m.heads)
	m.store.liveCount += m.live
	m.store.blockCount += m.blocks
	m.store.set.fireHooks(m.touched)
	return nil
}

// mergeDelta merges a plain delta and returns the parents to walk. The caller
// holds store.mu.
func (store *Datastore) mergeDelta(ctx context.Context, c cid.Cid, delta *pb.Delta) ([]link, error) {
	d := dotFromPB(delta.GetDot())
	if d.isZero() {
		return nil, fmt.Errorf("delta %s has no dot", c)
	}
	if store.cc.covers(d) {
		return nil, nil
	}
	m := store.beginMerge()
	linked, err := m.linked(ctx, d)
	if err != nil {
		return nil, err
	}
	for _, tomb := range delta.GetTombstones() {
		if err := m.remove(ctx, tomb.GetKey(), dotFromPB(tomb.GetDot())); err != nil {
			return nil, err
		}
	}
	for _, e := range delta.GetElements() {
		el := element{key: e.GetKey(), dot: d, priority: delta.GetPriority(), value: e.GetValue()}
		if err := m.admit(ctx, el, store.cc); err != nil {
			return nil, err
		}
	}
	m.cc.add(d, delta.GetPrev())
	if err := m.dropPending(ctx, d); err != nil {
		return nil, err
	}
	walk, err := m.link(ctx, delta.GetParents())
	if err != nil {
		return nil, err
	}
	m.settleBlock(c, d, delta.GetPriority(), kindDelta, linked)
	return walk, m.commit(ctx)
}

// dropPending forgets the pending removals of d's elements: d is covered now,
// and every element of d that was named by one was refused on arrival.
func (m *merging) dropPending(ctx context.Context, d dot) error {
	entries, err := m.t.scan(ctx, m.store.set.pendingPrefix().ChildString(d.segment()))
	if err != nil {
		return err
	}
	for _, e := range entries {
		m.t.del(ds.RawKey(e.key))
	}
	return nil
}

// mergeSnapshot joins a snapshot's state, root and chunks, as one merge. The
// caller holds store.mu.
func (store *Datastore) mergeSnapshot(ctx context.Context, c cid.Cid, root *pb.Delta, chunks []cid.Cid, elems []*pb.Element) ([]link, *causalContext, error) {
	d := dotFromPB(root.GetDot())
	if d.isZero() {
		return nil, nil, fmt.Errorf("snapshot %s has no dot", c)
	}
	if store.cc.covers(d) {
		return nil, nil, nil
	}
	theirs := contextFromPB(root.GetContext())
	local := store.cc
	m := store.beginMerge()
	linked, err := m.linked(ctx, d)
	if err != nil {
		return nil, nil, err
	}

	inSnapshot := make(map[string]struct{}, len(elems))
	for _, e := range elems {
		el := element{key: e.GetKey(), dot: dotFromPB(e.GetDot()), priority: e.GetPriority(), value: e.GetValue()}
		if el.dot.isZero() || !theirs.covers(el.dot) {
			return nil, nil, fmt.Errorf("snapshot %s holds an element its context does not cover", c)
		}
		inSnapshot[m.store.set.elemKey(el.key, el.dot).String()] = struct{}{}
		if err := m.admit(ctx, el, local); err != nil {
			return nil, nil, err
		}
	}

	// Ours that the snapshot covers and does not hold, it removed.
	var gone []element
	err = scanStore(ctx, store.store, store.set.elemsPrefix(), func(k string, v []byte) error {
		if _, ok := inSnapshot[k]; ok {
			return nil
		}
		el, err := store.set.parseElem(k, v)
		if err != nil {
			return err
		}
		if theirs.covers(el.dot) {
			gone = append(gone, el)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	for _, el := range gone {
		m.removeElem(el.key, el.dot)
	}

	m.cc.merge(theirs)
	m.cc.add(d, root.GetPrev())
	for _, tomb := range root.GetTombstones() {
		if err := m.remove(ctx, tomb.GetKey(), dotFromPB(tomb.GetDot())); err != nil {
			return nil, nil, err
		}
	}

	if err := m.dropCovered(ctx); err != nil {
		return nil, nil, err
	}
	walk, err := m.link(ctx, root.GetParents())
	if err != nil {
		return nil, nil, err
	}
	// The snapshot holds every head it covers: they are no longer tips.
	hs, _ := store.heads.List()
	for _, h := range hs {
		if theirs.covers(h.Dot) {
			m.heads.removeHead(h.Cid)
		}
	}
	m.settleBlock(c, d, root.GetPriority(), kindSnapshot, linked)
	for _, ch := range chunks {
		m.indexBlock(ch, kindChunk, d)
	}
	return walk, theirs, m.commit(ctx)
}

// dropCovered forgets every pending removal and missing parent the context
// now covers.
func (m *merging) dropCovered(ctx context.Context) error {
	pending, err := m.t.scan(ctx, m.store.set.pendingPrefix())
	if err != nil {
		return err
	}
	for _, e := range pending {
		_, d, err := m.store.set.parsePending(e.key)
		if err != nil {
			return err
		}
		if m.cc.covers(d) {
			m.t.del(ds.RawKey(e.key))
		}
	}
	missing, err := m.t.scan(ctx, m.store.missingPrefix())
	if err != nil {
		return err
	}
	for _, e := range missing {
		d, err := parseDotSegment(strings.TrimPrefix(e.key, m.store.missingPrefix().String()+"/"))
		if err != nil {
			return err
		}
		if m.cc.covers(d) {
			m.t.del(ds.RawKey(e.key))
		}
	}
	return nil
}

// purge drops every local block a snapshot's context covers, except the
// heads: the snapshot holds their state, and a reader reaching one through a
// link sees its dot covered and does not fetch it. It returns how many
// blocks it dropped. The caller holds store.mu.
func (store *Datastore) purge(ctx context.Context, covered *causalContext) (int, error) {
	var drop []cid.Cid
	var keys []ds.Key
	prefix := store.blocksPrefix()
	err := scanStore(ctx, store.store, prefix, func(k string, v []byte) error {
		_, d, err := decodeBlock(v)
		if err != nil {
			return err
		}
		if !covered.covers(d) {
			return nil
		}
		c, err := cidOfBlockKey(prefix, k)
		if err != nil {
			return err
		}
		if _, isHead := store.heads.Get(c); isHead {
			return nil
		}
		drop = append(drop, c)
		keys = append(keys, ds.RawKey(k))
		return nil
	})
	if err != nil || len(drop) == 0 {
		return 0, err
	}
	if err := store.dagService.RemoveMany(ctx, drop); err != nil {
		return 0, fmt.Errorf("error removing purged blocks: %w", err)
	}
	t := newTxn(store.store)
	for _, k := range keys {
		t.del(k)
	}
	if err := t.commit(ctx); err != nil {
		return 0, err
	}
	store.blockCount -= len(drop)
	return len(drop), nil
}
