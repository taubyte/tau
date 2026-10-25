package kvdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
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
// replica that compacted.
//
// What a context knows of an incarnation is forgotten once it is no longer
// needed (see retire), and an element whose dot no context covers any more is
// ancient: it is kept under its own dot, admitted by union, and removed by a
// pending removal that lives as long as the removal is needed. The join is
// the lattice join of what is still known; forgetting is the one step outside
// it, and it only forgets what no replica in contact can still be shown
// without (see the package's time-bound notes).

type blockKind byte

const (
	kindDelta blockKind = iota
	kindSnapshot
	kindChunk
)

// A link is a block a delta names, with its dot and stamps.
type link struct {
	cid     cid.Cid
	dot     dot
	at, ttl uint64
}

// A block is what the index holds of a local block.
type block struct {
	kind    blockKind
	dot     dot
	at, ttl uint64
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

func (store *Datastore) vectorPrefix() ds.Key {
	return store.namespace.ChildString(contextNs).ChildString("v")
}

func (store *Datastore) cloudPrefix() ds.Key {
	return store.namespace.ChildString(contextNs).ChildString("d")
}

func (store *Datastore) vectorKey(replica string) ds.Key {
	return store.vectorPrefix().ChildString(replicaSegment(replica))
}

// cloudKey is where the span (lo, hi] of replica is kept: under its end.
func (store *Datastore) cloudKey(replica string, hi uint64) ds.Key {
	return store.cloudPrefix().ChildString(dot{replica: replica, counter: hi}.segment())
}

func cidOfBlockKey(prefix ds.Key, k string) (cid.Cid, error) {
	return dshelp.DsKeyToCidV1(ds.NewKey(strings.TrimPrefix(k, prefix.String())), cid.DagProtobuf)
}

func encodeBlock(b block) []byte {
	v := append([]byte{byte(b.kind)}, encodeStamps(b.at, b.ttl)...)
	return append(v, encodeDot(b.dot)...)
}

func decodeBlock(v []byte) (block, error) {
	if len(v) < 2 {
		return block{}, errors.New("error decoding block entry")
	}
	st, rest, err := uvarints(v[1:], 2)
	if err != nil {
		return block{}, errors.New("error decoding block entry")
	}
	d, err := decodeDot(rest)
	return block{kind: blockKind(v[0]), dot: d, at: st[0], ttl: st[1]}, err
}

// encodeLink is the value of a missing parent or a retry mark: the block's
// stamps, then its CID.
func encodeLink(l link) []byte {
	return append(encodeStamps(l.at, l.ttl), l.cid.Bytes()...)
}

func decodeLink(d dot, v []byte) (link, error) {
	st, rest, err := uvarints(v, 2)
	if err != nil {
		return link{}, errors.New("error decoding link")
	}
	c, err := cid.Cast(rest)
	if err != nil {
		return link{}, err
	}
	return link{cid: c, dot: d, at: st[0], ttl: st[1]}, nil
}

func encodeEntry(k *known) []byte {
	b := binary.AppendUvarint(nil, k.spans[0].lo)
	b = binary.AppendUvarint(b, k.spans[0].hi)
	b = binary.AppendUvarint(b, k.changed)
	return binary.AppendUvarint(b, k.expires)
}

func (store *Datastore) loadContext(ctx context.Context) (*causalContext, error) {
	c := newCausalContext()
	vp := store.vectorPrefix()
	err := scanStore(ctx, store.store, vp, func(k string, v []byte) error {
		seg := strings.TrimPrefix(k, vp.String()+"/")
		replica, err := segEncoding.DecodeString(strings.TrimPrefix(seg, "r"))
		if err != nil {
			return err
		}
		f, _, err := uvarints(v, 4)
		if err != nil || f[1] <= f[0] {
			return errors.New("error decoding vector entry")
		}
		c.inc[string(replica)] = &known{spans: []span{{lo: f[0], hi: f[1]}}, changed: f[2], expires: f[3]}
		return nil
	})
	if err != nil {
		return nil, err
	}
	dp := store.cloudPrefix()
	err = scanStore(ctx, store.store, dp, func(k string, v []byte) error {
		d, err := parseDotSegment(strings.TrimPrefix(k, dp.String()+"/"))
		if err != nil {
			return err
		}
		lo, n := binary.Uvarint(v)
		if n <= 0 {
			return errors.New("error decoding cloud entry")
		}
		if kn := c.inc[d.replica]; kn != nil {
			kn.insert(span{lo: lo, hi: d.counter})
		}
		return nil
	})
	return c, err
}

// stageContext writes the difference between two contexts, forgotten
// incarnations included.
func (store *Datastore) stageContext(t *txn, old, cur *causalContext) {
	for r, o := range old.inc {
		if cur.inc[r] == nil {
			t.del(store.vectorKey(r))
			for _, s := range o.spans[1:] {
				t.del(store.cloudKey(r, s.hi))
			}
		}
	}
	for r, k := range cur.inc {
		o := old.inc[r]
		if o == nil || o.spans[0] != k.spans[0] || o.changed != k.changed || o.expires != k.expires {
			t.put(store.vectorKey(r), encodeEntry(k))
		}
		was := map[uint64]uint64{}
		if o != nil {
			for _, s := range o.spans[1:] {
				was[s.hi] = s.lo
			}
		}
		for _, s := range k.spans[1:] {
			if lo, ok := was[s.hi]; !ok || lo != s.lo {
				t.put(store.cloudKey(r, s.hi), binary.AppendUvarint(nil, s.lo))
			}
			delete(was, s.hi)
		}
		for hi := range was {
			t.del(store.cloudKey(r, hi))
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
	horizon uint64
	since   uint64
	// added lowers the store's lows by what this merge adds; scanned, when
	// set, is what a scan in it found and replaces them.
	added   lows
	scanned lows
}

func (store *Datastore) beginMerge() *merging {
	return &merging{
		store:   store,
		t:       newTxn(store.store),
		cc:      store.cc.clone(),
		heads:   newHeadChanges(),
		touched: map[string]*change{},
		horizon: store.horizon,
		since:   store.since,
		added:   lows{none, none, none},
	}
}

// heard takes what a block written by another incarnation says about fleet
// time: its stamp, no further ahead of this replica's clock than the skew.
// A replica that merged anything of anyone else's has heard a fleet.
func (m *merging) heard(author string, at uint64) {
	if author == m.store.replica || m.store.rejoining {
		return
	}
	m.since = max(m.since, min(at, m.store.clock()+m.store.times.skew))
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

func (m *merging) addElem(e element) {
	m.t.put(m.store.set.elemKey(e.key, e.dot), encodeMarker(e))
	m.touch(e.key)
	m.live++
}

// removeElem takes (key, d) out if it is here, and reports whether it was.
func (m *merging) removeElem(ctx context.Context, key string, d dot) (bool, error) {
	here, err := m.hasElem(ctx, key, d)
	if err != nil || !here {
		return false, err
	}
	m.t.del(m.store.set.elemKey(key, d))
	m.touch(key)
	m.live--
	return true, nil
}

// remove applies a tombstone. The element goes if it is here. If the context
// covers its dot, the context is what refutes any later copy of it; if not,
// whether the element was here or not, a pending removal does, for as long as
// the removal is needed: a copy may still be on its way, or held by a replica
// whose snapshot has not reached this one yet.
func (m *merging) remove(ctx context.Context, r removal) error {
	if _, err := m.removeElem(ctx, r.key, r.dot); err != nil {
		return err
	}
	if m.cc.covers(r.dot) {
		m.cc.touch(r.dot.replica, r.at, r.expires())
		return nil
	}
	pk := m.store.set.pendingKey(r.key, r.dot)
	if v, err := m.t.get(ctx, pk); err == nil {
		at, ttl, err := decodeStamps(v)
		if err != nil {
			return err
		}
		if old := (removal{at: at, ttl: ttl}); old.expires() > r.expires() || (old.expires() == r.expires() && old.at >= r.at) {
			return nil
		}
	} else if !errors.Is(err, ds.ErrNotFound) {
		return err
	}
	m.t.put(pk, encodeStamps(r.at, r.ttl))
	m.added.pending = min(m.added.pending, r.expires())
	return nil
}

// admit adds an element unless local, the context before this merge, covers
// it and it is not here (it was removed), or a removal of it is pending.
func (m *merging) admit(ctx context.Context, e element, local *causalContext) error {
	here, err := m.hasElem(ctx, e.key, e.dot)
	if err != nil || here || local.covers(e.dot) {
		return err
	}
	pending, err := m.t.has(ctx, m.store.set.pendingKey(e.key, e.dot))
	if err != nil || pending {
		return err
	}
	m.addElem(e)
	return nil
}

// link records a merged block's parents: a covered parent is no longer a
// head, an uncovered one is missing and is returned to be walked, unless it
// has expired, when no holder keeps it any more.
func (m *merging) link(ctx context.Context, parents []*pb.Parent) ([]link, error) {
	now := m.store.dropClock()
	var walk []link
	for _, p := range parents {
		c, err := cid.Cast(p.GetCid())
		if err != nil {
			return nil, fmt.Errorf("bad parent cid: %w", err)
		}
		l := link{cid: c, dot: dotFromPB(p.GetDot()), at: p.GetAt(), ttl: p.GetTtl()}
		if _, isHead := m.store.heads.Get(c); isHead {
			m.heads.removeHead(c)
		}
		if m.cc.covers(l.dot) || m.store.expired(l.at+l.ttl, now) {
			continue
		}
		m.t.put(m.store.missingKey(l.dot), encodeLink(l))
		walk = append(walk, l)
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
func (m *merging) settleBlock(c cid.Cid, b block, height uint64, linked bool) {
	m.t.del(m.store.missingKey(b.dot))
	m.t.del(m.store.retryKey(b.dot))
	if !linked {
		m.heads.addHead(Head{Cid: c, Height: height, Dot: b.dot, At: b.at, TTL: b.ttl})
	}
	m.indexBlock(c, b)
}

func (m *merging) indexBlock(c cid.Cid, b block) {
	m.t.put(m.store.blockKey(c), encodeBlock(b))
	m.added.block = min(m.added.block, b.at+b.ttl/2)
	m.blocks++
}

func (m *merging) commit(ctx context.Context) error {
	if err := m.store.set.settle(ctx, m.t, m.touched); err != nil {
		return err
	}
	m.store.stageContext(m.t, m.store.cc, m.cc)
	m.store.heads.stage(m.t, m.heads)
	if m.horizon != m.store.horizon {
		m.t.put(m.store.horizonKey(), binary.AppendUvarint(nil, m.horizon))
	}
	if m.since != m.store.since {
		m.t.put(m.store.sinceKey(), binary.AppendUvarint(nil, m.since))
	}
	if err := m.t.commit(ctx); err != nil {
		return err
	}
	m.store.horizon = m.horizon
	if m.scanned.pending != 0 {
		m.store.lows.pending = m.scanned.pending
	}
	m.store.lows.lower(m.added)
	if m.since != m.store.since {
		m.store.since, m.store.sinceUp = m.since, m.store.times.up()
	}
	up := m.store.times.up()
	for r, k := range m.cc.inc {
		if o := m.store.cc.inc[r]; o == nil || !slices.Equal(o.spans, k.spans) {
			m.store.merged[r] = up
		}
	}
	m.store.setContext(m.cc)
	m.store.heads.apply(m.heads)
	m.store.liveCount += m.live
	m.store.blockCount += m.blocks
	m.store.set.fireHooks(m.touched)
	return nil
}

// tombstoneOf reads a tombstone; one written without its own time takes the
// delta's.
func tombstoneOf(e *pb.Element, at uint64) removal {
	if e.GetAt() != 0 {
		at = e.GetAt()
	}
	return removal{key: e.GetKey(), dot: dotFromPB(e.GetDot()), at: at, ttl: e.GetTtl()}
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
	at, ttl := delta.GetAt(), delta.GetTtl()
	m := store.beginMerge()
	m.horizon = max(m.horizon, delta.GetHorizon())
	m.heard(d.replica, at)
	linked, err := m.linked(ctx, d)
	if err != nil {
		return nil, err
	}
	for _, tomb := range delta.GetTombstones() {
		if err := m.remove(ctx, tombstoneOf(tomb, at)); err != nil {
			return nil, err
		}
	}
	for _, e := range delta.GetElements() {
		el := element{key: e.GetKey(), dot: d, priority: delta.GetPriority(), value: e.GetValue(), at: at, ttl: ttl}
		if err := m.admit(ctx, el, store.cc); err != nil {
			return nil, err
		}
	}
	m.cc.add(d, delta.GetPrev(), at, at+ttl)
	if err := m.dropPending(ctx, d); err != nil {
		return nil, err
	}
	if d.replica == store.replica {
		if err := m.logOwn(d, delta); err != nil {
			return nil, err
		}
	}
	walk, err := m.link(ctx, delta.GetParents())
	if err != nil {
		return nil, err
	}
	m.settleBlock(c, block{kind: kindDelta, dot: d, at: at, ttl: ttl}, delta.GetPriority(), linked)
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
		if err := m.settlePending(e); err != nil {
			return err
		}
	}
	return nil
}

// settlePending forgets a pending removal whose dot the context now covers.
// What it knew passes to the context, so an incarnation's stamps are the same
// whether a removal of one of its dots arrived before the dot or after it.
func (m *merging) settlePending(e entry) error {
	r, err := m.store.set.parsePending(e.key, e.value)
	if err != nil {
		return err
	}
	m.cc.touch(r.dot.replica, r.at, r.expires())
	m.t.del(ds.RawKey(e.key))
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
	m.horizon = max(m.horizon, root.GetHorizon())
	m.heard(d.replica, root.GetAt())
	linked, err := m.linked(ctx, d)
	if err != nil {
		return nil, nil, err
	}

	inSnapshot := make(map[string]struct{}, len(elems))
	for _, e := range elems {
		el := element{key: e.GetKey(), dot: dotFromPB(e.GetDot()), priority: e.GetPriority(), value: e.GetValue(), at: e.GetAt(), ttl: e.GetTtl()}
		// An element its context does not cover is an ancient one, kept
		// under its own dot. It can even sit between two spans of its
		// incarnation, learned out of order after it was admitted, so
		// nothing short of the zero dot is malformed.
		if el.dot.isZero() {
			return nil, nil, fmt.Errorf("snapshot %s holds an element with no dot", c)
		}
		inSnapshot[m.store.set.elemKey(el.key, el.dot).String()] = struct{}{}
		if err := m.admit(ctx, el, local); err != nil {
			return nil, nil, err
		}
	}

	// Ours that the snapshot covers and does not hold, it removed. An
	// ancient element of ours is one it does not cover: it stays.
	held, err := m.t.scan(ctx, store.set.elemsPrefix())
	if err != nil {
		return nil, nil, err
	}
	var kept []element
	for _, e := range held {
		if _, ok := inSnapshot[e.key]; ok {
			continue
		}
		el, err := store.set.parseElem(e.key, e.value)
		if err != nil {
			return nil, nil, err
		}
		if !theirs.covers(el.dot) {
			kept = append(kept, el)
		} else if _, err := m.removeElem(ctx, el.key, el.dot); err != nil {
			return nil, nil, err
		}
	}

	m.cc.merge(theirs)
	at, ttl := root.GetAt(), root.GetTtl()
	m.cc.add(d, root.GetPrev(), at, at+ttl)
	for _, tomb := range root.GetTombstones() {
		if err := m.remove(ctx, tombstoneOf(tomb, at)); err != nil {
			return nil, nil, err
		}
	}
	// What the snapshot still knew and this replica had already forgotten
	// is forgotten again, at once: an entry lives as long as its stamps say,
	// not as long as someone keeps sending it.
	if _, err := m.forget(ctx, store.dropClock()); err != nil {
		return nil, nil, err
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
	b := block{kind: kindSnapshot, dot: d, at: at, ttl: ttl}
	m.settleBlock(c, b, root.GetPriority(), linked)
	for _, ch := range chunks {
		m.indexBlock(ch, block{kind: kindChunk, dot: d, at: at, ttl: ttl})
	}
	differs := false
	if !linked && m.collapses(hs) {
		want := theirs.clone()
		want.add(d, root.GetPrev(), at, at+ttl)
		if differs, err = m.differs(ctx, want, elems, kept, root.GetTombstones(), at); err != nil {
			return nil, nil, err
		}
	}
	if err := m.commit(ctx); err != nil {
		return nil, nil, err
	}
	if differs {
		store.compactDue = true
	}
	return walk, theirs, nil
}

// collapses reports whether a merge leaves none of the heads hs it started
// from: the block it merged is then the only one that tells a reader what
// this replica holds.
func (m *merging) collapses(hs []Head) bool {
	for _, h := range hs {
		if _, gone := m.heads.remove[h.Cid]; !gone {
			return false
		}
	}
	return true
}

// differs reports whether a snapshot merge leaves this replica with a state
// other than the snapshot's, want being the snapshot's context with its own
// dot: an element it holds refused, one it neither holds nor covers kept, or
// a context entry, span, stamp or pending removal it lacks. With the
// snapshot the only head, nothing else would ever carry the difference to a
// replica that holds the snapshot, so the replica compacts. Snapshots that
// agree, concurrent ones included, never answer each other.
func (m *merging) differs(ctx context.Context, want *causalContext, elems []*pb.Element, kept []element, tombs []*pb.Element, at uint64) (bool, error) {
	for r, k := range m.cc.inc {
		o := want.inc[r]
		if o == nil || !slices.Equal(o.spans, k.spans) || k.changed > o.changed || k.expires > o.expires {
			return true, nil
		}
	}
	carried := make(map[string]removal, len(tombs))
	for _, tomb := range tombs {
		r := tombstoneOf(tomb, at)
		carried[m.store.set.pendingKey(r.key, r.dot).String()] = r
	}
	pending, err := m.t.scan(ctx, m.store.set.pendingPrefix())
	if err != nil {
		return false, err
	}
	for _, e := range pending {
		r, err := m.store.set.parsePending(e.key, e.value)
		if err != nil {
			return false, err
		}
		if o, ok := carried[e.key]; !ok || r.expires() > o.expires() || (r.expires() == o.expires() && r.at > o.at) {
			return true, nil
		}
	}
	for _, el := range kept {
		if here, err := m.hasElem(ctx, el.key, el.dot); err != nil || here {
			return here, err
		}
	}
	for _, e := range elems {
		if here, err := m.hasElem(ctx, e.GetKey(), dotFromPB(e.GetDot())); err != nil || !here {
			return !here, err
		}
	}
	return false, nil
}

// dropCovered forgets every pending removal, missing parent and retry mark
// the context now covers.
func (m *merging) dropCovered(ctx context.Context) error {
	pending, err := m.t.scan(ctx, m.store.set.pendingPrefix())
	if err != nil {
		return err
	}
	for _, e := range pending {
		r, err := m.store.set.parsePending(e.key, e.value)
		if err != nil {
			return err
		}
		if m.cc.covers(r.dot) {
			if err := m.settlePending(e); err != nil {
				return err
			}
		}
	}
	for _, prefix := range []ds.Key{m.store.missingPrefix(), m.store.retryPrefix()} {
		marks, err := m.t.scan(ctx, prefix)
		if err != nil {
			return err
		}
		for _, e := range marks {
			d, err := parseDotSegment(strings.TrimPrefix(e.key, prefix.String()+"/"))
			if err != nil {
				return err
			}
			if m.cc.covers(d) {
				m.t.del(ds.RawKey(e.key))
			}
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
		b, err := decodeBlock(v)
		if err != nil {
			return err
		}
		if !covered.covers(b.dot) {
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
	return len(drop), store.dropBlocks(ctx, drop, keys)
}

// dropBlocks removes blocks and their index entries.
func (store *Datastore) dropBlocks(ctx context.Context, drop []cid.Cid, keys []ds.Key) error {
	if err := store.dagService.RemoveMany(ctx, drop); err != nil {
		return fmt.Errorf("error removing purged blocks: %w", err)
	}
	t := newTxn(store.store)
	for _, k := range keys {
		t.del(k)
	}
	if err := t.commit(ctx); err != nil {
		return err
	}
	store.blockCount -= len(drop)
	return nil
}
