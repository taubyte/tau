package kvdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"

	dshelp "github.com/ipfs/boxo/datastore/dshelp"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/taubyte/tau/pkg/kvdb/mst"
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
)

// A link is a block a delta names, with its dot and stamps.
type link struct {
	cid     cid.Cid
	dot     dot
	at, ttl uint64
}

// A block is what the index holds of a local block. A merged snapshot's
// names its state, which it holds a reference to.
type block struct {
	kind    blockKind
	dot     dot
	at, ttl uint64
	state   cid.Cid
}

func (store *Datastore) blocksPrefix() ds.Key { return ds.RawKey(store.pre.blocks) }

func (store *Datastore) blockKey(c cid.Cid) ds.Key { return hashKey(store.pre.blocks, c) }

func (store *Datastore) missingPrefix() ds.Key { return ds.RawKey(store.pre.missing) }

func (store *Datastore) missingKey(d dot) ds.Key { return dotKey(store.pre.missing, d) }

func (store *Datastore) vectorPrefix() ds.Key { return ds.RawKey(store.pre.vector) }

func (store *Datastore) cloudPrefix() ds.Key { return ds.RawKey(store.pre.cloud) }

func (store *Datastore) vectorKey(replica string) ds.Key {
	b := make([]byte, 0, len(store.pre.vector)+1+replicaSegmentLen(replica))
	return dsKey(appendReplicaSegment(append(append(b, store.pre.vector...), '/'), replica))
}

// cloudKey is where the span (lo, hi] of replica is kept: under its end.
func (store *Datastore) cloudKey(replica string, hi uint64) ds.Key {
	return dotKey(store.pre.cloud, dot{replica: replica, counter: hi})
}

func cidOfBlockKey(prefix ds.Key, k string) (cid.Cid, error) {
	return dshelp.DsKeyToCidV1(ds.NewKey(strings.TrimPrefix(k, prefix.String())), cid.DagProtobuf)
}

// encodeBlock is the kind, the stamps, the state's cid with its length, and
// the dot, in one allocation.
func encodeBlock(b block) []byte {
	var sc string
	if b.state.Defined() {
		sc = b.state.KeyString()
	}
	v := make([]byte, 0, 1+4*binary.MaxVarintLen64+len(sc)+len(b.dot.replica))
	v = binary.AppendUvarint(binary.AppendUvarint(append(v, byte(b.kind)), b.at), b.ttl)
	v = append(binary.AppendUvarint(v, uint64(len(sc))), sc...)
	return append(binary.AppendUvarint(v, b.dot.counter), b.dot.replica...)
}

func decodeBlock(v []byte) (block, error) {
	if len(v) < 2 {
		return block{}, errors.New("error decoding block entry")
	}
	st, rest, err := uvarints(v[1:], 3)
	if err != nil || uint64(len(rest)) < st[2] {
		return block{}, errors.New("error decoding block entry")
	}
	b := block{kind: blockKind(v[0]), at: st[0], ttl: st[1]}
	if st[2] > 0 {
		if b.state, err = cid.Cast(rest[:st[2]]); err != nil {
			return block{}, err
		}
	}
	b.dot, err = decodeDot(rest[st[2]:])
	return b, err
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
	// pendingAt is the latest stamp of a pending removal this merge files,
	// or of all it leaves once rescanned is set.
	pendingAt uint64
	rescanned bool
	// unerased are the erased records this merge forgets.
	unerased []string
	// atomic commits with commitAtomic: the merge changes the roots that
	// keep state blocks.
	atomic bool
	// base is the state base the merge leaves, when it moves it.
	base *stateBase
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
	if m.refutes(r) {
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
	m.pendingAt = max(m.pendingAt, r.at)
	return nil
}

// refutes reports whether the context refutes r's dot for it: it covers the
// dot under an entry that had not expired when r was written. One expired by
// then counts as forgotten, as it is wherever a drop clock has reached that
// expiry, so that whether a removal touches the entry or is pending is
// decided by the removal's own time, not by the receiver's clock.
func (m *merging) refutes(r removal) bool {
	if !m.cc.covers(r.dot) {
		return false
	}
	k := m.cc.inc[r.dot.replica]
	return k == nil || !m.store.expired(k.expires, r.at)
}

// admit adds an element unless local, the context before this merge, covers
// it and it is not here (it was removed), or a removal of it is pending. One
// a rebase erased is removed instead, by a pending removal that the next
// snapshot of this replica, due now, carries to the replicas that hold it.
func (m *merging) admit(ctx context.Context, e element, local *causalContext) error {
	here, err := m.hasElem(ctx, e.key, e.dot)
	if err != nil || here || local.covers(e.dot) {
		return err
	}
	pending, err := m.t.has(ctx, m.store.set.pendingKey(e.key, e.dot))
	if err != nil || pending {
		return err
	}
	if k := m.store.erased[e.dot.replica]; k != nil && k.covers(e.dot.counter) {
		m.store.compactDue = true
		return m.remove(ctx, removal{key: e.key, dot: e.dot, at: k.changed, ttl: k.expires - k.changed})
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
	m.t.del(m.store.reclaimKey(c))
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
	commit := m.t.commit
	if m.atomic {
		commit = func(ctx context.Context) error { return m.store.commitAtomic(ctx, m.t) }
	}
	if err := commit(ctx); err != nil {
		return err
	}
	if m.base != nil {
		m.store.base = *m.base
	}
	m.store.horizon = m.horizon
	for _, r := range m.unerased {
		delete(m.store.erased, r)
	}
	if m.scanned.pending != 0 {
		m.store.lows.pending = m.scanned.pending
	}
	m.store.lows.lower(m.added)
	if m.rescanned {
		m.store.pendingAt = m.pendingAt
	} else {
		m.store.pendingAt = max(m.store.pendingAt, m.pendingAt)
	}
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
	return removal{key: stringOf(e.GetKey()), dot: dotFromPB(e.GetDot()), at: at, ttl: e.GetTtl()}
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
		el := element{key: stringOf(e.GetKey()), dot: d, priority: delta.GetPriority(), value: e.GetValue(), at: at, ttl: ttl}
		if err := m.admit(ctx, el, store.cc); err != nil {
			return nil, err
		}
	}
	m.cc.add(d, delta.GetPrev(), at, at+ttl)
	if err := m.dropPending(ctx, d); err != nil {
		return nil, err
	}
	if err := m.dropExtended(ctx, store.cc); err != nil {
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
	err = m.commit(ctx)
	m.t.release()
	return walk, err
}

// dropPending forgets the pending removals of d's elements the context now
// refutes: every element of d that was named by one was refused on arrival.
func (m *merging) dropPending(ctx context.Context, d dot) error {
	entries, err := m.t.scan(ctx, dotKey(m.store.set.pending, d))
	if err != nil {
		return err
	}
	for _, e := range entries {
		r, err := m.store.set.parsePending(e.key, e.value)
		if err != nil {
			return err
		}
		if m.refutes(r) {
			m.settlePending(e.key, r)
		}
	}
	return nil
}

// dropExtended forgets the pending removals an entry this merge extended now
// refutes: one filed because the entry, as this replica then knew it, had
// expired when the removal was written, which a later delta of the
// incarnation shows it had not. A replica that merged that delta first
// touched the entry instead. Only a removal stamped at or after the entry's
// old expiry can be one, so the pending removals are scanned only when one
// may be stamped that late.
func (m *merging) dropExtended(ctx context.Context, local *causalContext) error {
	late := max(m.store.pendingAt, m.pendingAt)
	extended := false
	for r, k := range m.cc.inc {
		if o := local.inc[r]; o != nil && k.expires > o.expires && o.expires+m.store.times.margin <= late {
			extended = true
			break
		}
	}
	if !extended {
		return nil
	}
	entries, err := m.t.scan(ctx, m.store.set.pendingPrefix())
	if err != nil {
		return err
	}
	m.pendingAt, m.rescanned = 0, true
	for _, e := range entries {
		r, err := m.store.set.parsePending(e.key, e.value)
		if err != nil {
			return err
		}
		if m.refutes(r) {
			m.settlePending(e.key, r)
			continue
		}
		m.pendingAt = max(m.pendingAt, r.at)
	}
	return nil
}

// settlePending forgets a pending removal the context now refutes.
// What it knew passes to the context, so an incarnation's stamps are the same
// whether a removal of one of its dots arrived before the dot or after it.
func (m *merging) settlePending(key string, r removal) {
	m.cc.touch(r.dot.replica, r.at, r.expires())
	m.t.del(ds.RawKey(key))
}

// A snapState is the state a snapshot carries, read from its trees.
type snapState struct {
	root           cid.Cid
	sr             stateRoot
	elems, pending *mst.Tree
	cc             *causalContext
	// look reads records of elems and pending, sharing the nodes on the
	// paths it has read.
	lookElems, lookPending *mst.Editor
}

func (s *snapState) elem(ctx context.Context, tk []byte) ([]byte, bool, error) {
	if s.lookElems == nil {
		s.lookElems = s.elems.Edit()
	}
	return s.lookElems.Get(ctx, tk)
}

func (s *snapState) removal(ctx context.Context, tk []byte) ([]byte, bool, error) {
	if s.lookPending == nil {
		s.lookPending = s.pending.Edit()
	}
	return s.lookPending.Get(ctx, tk)
}

func (store *Datastore) readState(ctx context.Context, dags ipld.DAGService, c cid.Cid) (*snapState, error) {
	sr, err := loadStateRoot(ctx, dags, c)
	if err != nil {
		return nil, err
	}
	s := &snapState{root: c, sr: sr, elems: store.tree(dags, sr.elems), pending: store.tree(dags, sr.pending)}
	if s.cc, err = readContext(ctx, store.tree(dags, sr.ctx)); err != nil {
		return nil, fmt.Errorf("state %s: %w", c, err)
	}
	return s, nil
}

// An elemCase is an element a snapshot merge decides, with the snapshot's
// record of it, nil when it holds none; a pendCase the same of a pending
// removal under one tree key.
type elemCase struct {
	key    string
	dot    dot
	theirs *element
}

type pendCase struct {
	key    string
	dot    dot
	theirs *removal
}

// A mergeSet is every record a snapshot merge has to decide, by tree key:
// those whose record may differ between this replica and the snapshot. The
// join keeps any other record as it is, since both hold it alike.
type mergeSet struct {
	elems   map[string]elemCase
	pending map[string]pendCase
	// pendingOf is the tree keys pending names for each removal, journal
	// the number of marks the journal holds.
	pendingOf map[string][]string
	journal   int
}

func newMergeSet() *mergeSet {
	return &mergeSet{elems: map[string]elemCase{}, pending: map[string]pendCase{}, pendingOf: map[string][]string{}}
}

func (ms *mergeSet) addElem(tk []byte, theirs []byte, has bool) error {
	if _, ok := ms.elems[string(tk)]; ok {
		return nil
	}
	key, d, err := parseElemTreeKey(tk)
	if err != nil {
		return err
	}
	c := elemCase{key: key, dot: d}
	if has {
		el, err := parseElemValue(key, d, theirs)
		if err != nil {
			return err
		}
		c.theirs = &el
	}
	ms.elems[string(tk)] = c
	return nil
}

func (ms *mergeSet) addPending(tk []byte, theirs []byte, has bool) error {
	if _, ok := ms.pending[string(tk)]; ok {
		return nil
	}
	_, key, d, err := parsePendingTreeKey(tk)
	if err != nil {
		return err
	}
	c := pendCase{key: key, dot: d}
	if has {
		r, err := parsePending(tk, theirs)
		if err != nil {
			return err
		}
		c.theirs = &r
	}
	ms.pending[string(tk)] = c
	id := idOf(key, d)
	ms.pendingOf[id] = append(ms.pendingOf[id], string(tk))
	return nil
}

// enumerate is the records whose trees differ between the base and s, and
// those the journal names: what this replica holds differs from the base
// in nothing else.
func (store *Datastore) enumerate(ctx context.Context, s *snapState) (*mergeSet, error) {
	ms := newMergeSet()
	err := mst.Diff(ctx, store.tree(store.dagService, store.base.elems), s.elems, func(ch mst.Change) error {
		return ms.addElem(ch.Key, ch.New, ch.Kind != mst.Removed)
	})
	if err != nil {
		return nil, err
	}
	err = mst.Diff(ctx, store.tree(store.dagService, store.base.pending), s.pending, func(ch mst.Change) error {
		return ms.addPending(ch.Key, ch.New, ch.Kind != mst.Removed)
	})
	if err != nil {
		return nil, err
	}
	err = scanStore(ctx, store.store, store.journal.elemsPrefix(), func(k string, _ []byte) error {
		key, d, err := store.journal.parseElemMark(k)
		if err != nil {
			return err
		}
		ms.journal++
		tk := elemTreeKey(key, d)
		v, has, err := s.elem(ctx, tk)
		if err != nil {
			return err
		}
		return ms.addElem(tk, v, has)
	})
	if err != nil {
		return nil, err
	}
	err = scanStore(ctx, store.store, store.journal.pendingPrefix(), func(k string, _ []byte) error {
		b, key, d, err := store.journal.parsePendingMark(k)
		if err != nil {
			return err
		}
		ms.journal++
		tk := pendingTreeKeyIn(b, key, d)
		v, has, err := s.removal(ctx, tk)
		if err != nil {
			return err
		}
		return ms.addPending(tk, v, has)
	})
	return ms, err
}

func idOf(key string, d dot) string { return string(elemTreeKey(key, d)) }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// mergeSnapshot joins the state a snapshot carries, whose blocks dags holds,
// as one merge. Only the records its trees and this replica's may disagree
// on are read (see enumerate); every other one both hold alike. The state's
// closure is what the roots reach and sess's intents: the merge publishes
// them, with the root that now reaches them. The caller holds store.mu.
func (store *Datastore) mergeSnapshot(ctx context.Context, c cid.Cid, root *pb.Delta, dags ipld.DAGService, sess *gcSession) ([]link, *causalContext, error) {
	d := dotFromPB(root.GetDot())
	if d.isZero() {
		return nil, nil, fmt.Errorf("snapshot %s has no dot", c)
	}
	if store.cc.covers(d) {
		return nil, nil, nil
	}
	stc, err := cid.Cast(root.GetState())
	if err != nil {
		return nil, nil, fmt.Errorf("snapshot %s has no state: %w", c, err)
	}
	s, err := store.readState(ctx, dags, stc)
	if err != nil {
		return nil, nil, err
	}
	ms, err := store.enumerate(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	p, err := store.prepareSnapshot(ctx, c, root, s, ms)
	if err != nil {
		return nil, nil, err
	}
	if checkSnapshot != nil {
		if err := checkSnapshot(ctx, store, c, root, s, p); err != nil {
			return nil, nil, err
		}
	}
	m := p.m
	store.gcMu.Lock()
	defer store.gcMu.Unlock()
	if err := store.drainLocked(ctx); err != nil {
		return nil, nil, err
	}
	if !store.gc.reaches(sess, stc) {
		return nil, nil, fmt.Errorf("snapshot %s: its state is not held", c)
	}
	ch := store.changeRoots()
	ch.addState(stc, s.sr)
	// The base moves to the snapshot's trees when that leaves no more to
	// apply than the journal holds now: they are the trees a peer holds,
	// and the next snapshot then shares them.
	if len(p.residual) <= ms.journal {
		if err := m.rebase(ctx, ch, s, p.residual); err != nil {
			return nil, nil, err
		}
	}
	if sess != nil && !sess.published {
		ch.publish(sess, m.t)
	}
	if err := ch.prepare(ctx, m.t); err != nil {
		return nil, nil, err
	}
	// What the commit makes a root reach is durable before it does.
	if err := store.syncBlocks(ctx); err != nil {
		return nil, nil, err
	}
	if err := m.commit(ctx); err != nil {
		return nil, nil, err
	}
	ch.apply()
	if sess != nil && !sess.published {
		store.published(sess)
	}
	if p.differs {
		store.compactDue = true
	}
	return p.walk, s.cc, nil
}

// checkSnapshot, when set, is shown every snapshot merge before it commits.
var checkSnapshot func(ctx context.Context, store *Datastore, c cid.Cid, root *pb.Delta, s *snapState, p *snapshotMerge) error

// A snapshotMerge is a snapshot merge decided and not yet committed: its
// writes, the parents to walk, whether it leaves a state other than the
// snapshot's, and the journal it would leave with the snapshot's trees as
// the base.
type snapshotMerge struct {
	m        *merging
	walk     []link
	differs  bool
	residual map[string]bool
}

// prepareSnapshot decides the merge of s, deciding each record ms names.
// The caller holds store.mu.
func (store *Datastore) prepareSnapshot(ctx context.Context, c cid.Cid, root *pb.Delta, s *snapState, ms *mergeSet) (*snapshotMerge, error) {
	d := dotFromPB(root.GetDot())
	theirs := s.cc
	local := store.cc
	m := store.beginMerge()
	m.atomic = true
	m.horizon = max(m.horizon, root.GetHorizon())
	m.heard(d.replica, root.GetAt())
	linked, err := m.linked(ctx, d)
	if err != nil {
		return nil, err
	}
	// Ours that the snapshot covers and does not hold, it removed. An
	// ancient element of ours is one it does not cover: it stays.
	var kept []element
	for _, tk := range sortedKeys(ms.elems) {
		ec := ms.elems[tk]
		if ec.theirs != nil {
			if err := m.admit(ctx, *ec.theirs, local); err != nil {
				return nil, err
			}
			continue
		}
		here, err := m.hasElem(ctx, ec.key, ec.dot)
		if err != nil {
			return nil, err
		}
		if !here {
			continue
		}
		if !theirs.covers(ec.dot) {
			kept = append(kept, element{key: ec.key, dot: ec.dot})
		} else if _, err := m.removeElem(ctx, ec.key, ec.dot); err != nil {
			return nil, err
		}
	}

	m.cc.merge(theirs)
	at, ttl := root.GetAt(), root.GetTtl()
	m.cc.add(d, root.GetPrev(), at, at+ttl)
	now := store.dropClock()
	for _, tk := range sortedKeys(ms.pending) {
		// One that has expired here is not applied, as the delta that
		// wrote it would not be merged.
		if r := ms.pending[tk].theirs; r != nil && !store.expired(r.expires(), now) {
			if err := m.remove(ctx, *r); err != nil {
				return nil, err
			}
		}
	}
	// What the snapshot still knew and this replica had already forgotten
	// is forgotten again, at once: an entry lives as long as its stamps say,
	// not as long as someone keeps sending it.
	if _, err := m.forget(ctx, now); err != nil {
		return nil, err
	}

	if err := m.dropCovered(ctx); err != nil {
		return nil, err
	}
	walk, err := m.link(ctx, root.GetParents())
	if err != nil {
		return nil, err
	}
	// The snapshot holds every head it covers: they are no longer tips.
	hs, _ := store.heads.List()
	for _, h := range hs {
		if theirs.covers(h.Dot) {
			m.heads.removeHead(h.Cid)
		}
	}
	m.settleBlock(c, block{kind: kindSnapshot, dot: d, at: at, ttl: ttl, state: s.root}, root.GetPriority(), linked)
	differs := false
	if !linked && m.collapses(hs) {
		want := theirs.clone()
		want.add(d, root.GetPrev(), at, at+ttl)
		if differs, err = m.differs(ctx, want, s, ms, kept); err != nil {
			return nil, err
		}
	}

	residual, err := m.residual(ctx, s, ms)
	if err != nil {
		return nil, err
	}
	return &snapshotMerge{m: m, walk: walk, differs: differs, residual: residual}, nil
}

// rebase moves the base to s's trees and makes the journal name exactly the
// records this replica holds otherwise than s, residual.
func (m *merging) rebase(ctx context.Context, ch *rootChange, s *snapState, residual map[string]bool) error {
	b := stateBase{elems: s.sr.elems, pending: s.sr.pending}
	ch.setBase(m.t, m.store.base, b)
	m.base = &b
	for _, prefix := range []ds.Key{m.store.journal.elemsPrefix(), m.store.journal.pendingPrefix()} {
		if err := scanStore(ctx, m.store.store, prefix, func(k string, _ []byte) error {
			if !residual[k] {
				m.t.del(ds.RawKey(k))
			}
			return nil
		}); err != nil {
			return err
		}
	}
	// What this merge writes is marked as it is written, after this scan.
	err := m.written(func(pending bool, key string, d dot) error {
		marks := []string{m.store.journal.elemMark(key, d)}
		if pending {
			marks = nil
			for _, tk := range m.pendingKeys(ctx, key, d, nil) {
				marks = append(marks, m.store.journal.pendingMarkIn(binary.BigEndian.Uint64(tk), key, d))
			}
		}
		for _, mk := range marks {
			if !residual[mk] {
				m.t.del(ds.RawKey(mk))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for mk := range residual {
		m.t.put(ds.RawKey(mk), nil)
	}
	return nil
}

// localElem is the element (key, d) this merge leaves, if any.
func (m *merging) localElem(ctx context.Context, key string, d dot) (*element, error) {
	v, err := m.t.get(ctx, m.store.set.elemKey(key, d))
	if errors.Is(err, ds.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	el, err := decodeMarker(v)
	if err != nil {
		return nil, err
	}
	el.key, el.dot = key, d
	return &el, nil
}

// localPending is the pending removal of (key, d) this merge leaves, if
// any.
func (m *merging) localPending(ctx context.Context, key string, d dot) (*removal, error) {
	v, err := m.t.get(ctx, m.store.set.pendingKey(key, d))
	if errors.Is(err, ds.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	at, ttl, err := decodeStamps(v)
	if err != nil {
		return nil, err
	}
	return &removal{key: key, dot: d, at: at, ttl: ttl}, nil
}

// written calls fn with the identity of every element and pending removal
// the merge writes.
func (m *merging) written(fn func(pending bool, key string, d dot) error) error {
	ep, pp := m.store.set.elemsPrefix().String()+"/", m.store.set.pendingPrefix().String()+"/"
	for _, k := range m.t.order {
		if rest, ok := strings.CutPrefix(k, ep); ok {
			kseg, dseg, _ := strings.Cut(rest, "/")
			key, err := parseKeySegment(kseg)
			if err != nil {
				return err
			}
			d, err := parseDotSegment(dseg)
			if err != nil {
				return err
			}
			if err := fn(false, key, d); err != nil {
				return err
			}
		} else if rest, ok := strings.CutPrefix(k, pp); ok {
			dseg, kseg, _ := strings.Cut(rest, "/")
			key, err := parseKeySegment(kseg)
			if err != nil {
				return err
			}
			d, err := parseDotSegment(dseg)
			if err != nil {
				return err
			}
			if err := fn(true, key, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// pendingKeys is every tree key a pending removal of (key, d) may sit under
// here or in s: the one this merge leaves, the one the store held, and
// those ms names for it.
func (m *merging) pendingKeys(ctx context.Context, key string, d dot, ms *mergeSet) [][]byte {
	var out [][]byte
	add := func(tk []byte) {
		if !slices.ContainsFunc(out, func(o []byte) bool { return bytes.Equal(o, tk) }) {
			out = append(out, tk)
		}
	}
	if mine, err := m.localPending(ctx, key, d); err == nil && mine != nil {
		add(pendingTreeKey(*mine))
	}
	if v, err := m.t.store.Get(ctx, m.store.set.pendingKey(key, d)); err == nil {
		if at, ttl, err := decodeStamps(v); err == nil {
			add(pendingTreeKey(removal{key: key, dot: d, at: at, ttl: ttl}))
		}
	}
	if ms != nil {
		for _, tk := range ms.pendingOf[idOf(key, d)] {
			add([]byte(tk))
		}
	}
	return out
}

// residual is the journal this replica would have with s's trees as its
// base: a mark for every record it holds otherwise than s. Only the records
// ms names and those the merge writes can differ: the base holds every
// other one as s does, and the journal names every record this replica
// changed since.
func (m *merging) residual(ctx context.Context, s *snapState, ms *mergeSet) (map[string]bool, error) {
	out := map[string]bool{}
	elemDone := map[string]bool{}
	checkElem := func(key string, d dot) error {
		tk := elemTreeKey(key, d)
		if elemDone[string(tk)] {
			return nil
		}
		elemDone[string(tk)] = true
		var theirs *element
		if ec, ok := ms.elems[string(tk)]; ok {
			theirs = ec.theirs
		} else {
			v, has, err := s.elem(ctx, tk)
			if err != nil {
				return err
			}
			if has {
				el, err := parseElemValue(key, d, v)
				if err != nil {
					return err
				}
				theirs = &el
			}
		}
		mine, err := m.localElem(ctx, key, d)
		if err != nil {
			return err
		}
		if (mine == nil) != (theirs == nil) || (mine != nil && !bytes.Equal(elemValue(*mine), elemValue(*theirs))) {
			out[m.store.journal.elemMark(key, d)] = true
		}
		return nil
	}
	pendDone := map[string]bool{}
	checkPending := func(key string, d dot) error {
		id := idOf(key, d)
		if pendDone[id] {
			return nil
		}
		pendDone[id] = true
		mine, err := m.localPending(ctx, key, d)
		if err != nil {
			return err
		}
		for _, tk := range m.pendingKeys(ctx, key, d, ms) {
			var theirs *removal
			if pc, ok := ms.pending[string(tk)]; ok {
				theirs = pc.theirs
			} else {
				v, has, err := s.removal(ctx, tk)
				if err != nil {
					return err
				}
				if has {
					r, err := parsePending(tk, v)
					if err != nil {
						return err
					}
					theirs = &r
				}
			}
			here := mine
			if here != nil && !bytes.Equal(pendingTreeKey(*here), tk) {
				here = nil
			}
			if (here == nil) != (theirs == nil) || (here != nil && *here != *theirs) {
				out[m.store.journal.pendingMarkIn(binary.BigEndian.Uint64(tk), key, d)] = true
			}
		}
		return nil
	}
	for _, ec := range ms.elems {
		if err := checkElem(ec.key, ec.dot); err != nil {
			return nil, err
		}
	}
	for _, pc := range ms.pending {
		if err := checkPending(pc.key, pc.dot); err != nil {
			return nil, err
		}
	}
	err := m.written(func(pending bool, key string, d dot) error {
		if pending {
			return checkPending(key, d)
		}
		return checkElem(key, d)
	})
	return out, err
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
// agree, concurrent ones included, never answer each other. Only the
// records ms names and those the merge writes can differ.
func (m *merging) differs(ctx context.Context, want *causalContext, s *snapState, ms *mergeSet, kept []element) (bool, error) {
	for r, k := range m.cc.inc {
		o := want.inc[r]
		if o == nil || !slices.Equal(o.spans, k.spans) || k.changed > o.changed || k.expires > o.expires {
			return true, nil
		}
	}
	carried := map[string]removal{}
	for _, pc := range ms.pending {
		if pc.theirs != nil {
			carried[m.store.set.pendingKey(pc.key, pc.dot).String()] = *pc.theirs
		}
	}
	lacks := func(key string, d dot) (bool, error) {
		r, err := m.localPending(ctx, key, d)
		if err != nil || r == nil {
			return false, err
		}
		o, ok := carried[m.store.set.pendingKey(key, d).String()]
		if !ok {
			v, has, err := s.removal(ctx, pendingTreeKey(*r))
			if err != nil || !has {
				return !has, err
			}
			if o, err = parsePending(pendingTreeKey(*r), v); err != nil {
				return false, err
			}
		}
		return r.expires() > o.expires() || (r.expires() == o.expires() && r.at > o.at), nil
	}
	for _, pc := range ms.pending {
		if l, err := lacks(pc.key, pc.dot); err != nil || l {
			return l, err
		}
	}
	found := false
	err := m.written(func(pending bool, key string, d dot) error {
		if !pending || found {
			return nil
		}
		l, err := lacks(key, d)
		found = l
		return err
	})
	if err != nil || found {
		return found, err
	}
	for _, el := range kept {
		if here, err := m.hasElem(ctx, el.key, el.dot); err != nil || here {
			return here, err
		}
	}
	for _, ec := range ms.elems {
		if ec.theirs == nil {
			continue
		}
		if here, err := m.hasElem(ctx, ec.key, ec.dot); err != nil || !here {
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
		if m.refutes(r) {
			m.settlePending(e.key, r)
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

// purge takes every local block a snapshot's context covers, except the
// heads: the snapshot holds their state, and a reader reaching one through a
// link sees its dot covered and does not fetch it. dropTaken removes them
// once the caller has released store.mu. The caller holds store.mu.
func (store *Datastore) purge(ctx context.Context, covered *causalContext) ([]cid.Cid, error) {
	return store.take(ctx, func(b block) bool { return covered.covers(b.dot) })
}

var errTaken = errors.New("being dropped by a purge")

// take takes the indexed blocks that pick chooses, other than the heads and
// the blocks a purge has taken already, out of the count, and returns them
// for dropTaken. Until it drops them a taken block is held by nothing: no
// walk counts it, no merge indexes it again and no compaction folds it. The
// caller holds store.mu.
func (store *Datastore) take(ctx context.Context, pick func(block) bool) ([]cid.Cid, error) {
	var taken []cid.Cid
	prefix := store.blocksPrefix()
	err := scanStore(ctx, store.store, prefix, func(k string, v []byte) error {
		b, err := decodeBlock(v)
		if err != nil {
			return err
		}
		if !pick(b) {
			return nil
		}
		c, err := cidOfBlockKey(prefix, k)
		if err != nil {
			return err
		}
		if _, isHead := store.heads.Get(c); isHead || store.purging.Has(c) {
			return nil
		}
		taken = append(taken, c)
		return nil
	})
	if err != nil || len(taken) == 0 || store.closed {
		return nil, err
	}
	for _, c := range taken {
		store.purging.Visit(c)
	}
	store.blockCount -= len(taken)
	store.dropping.Add(1)
	return taken, nil
}

// isTaken reports whether the index entry at k names a block a purge has
// taken.
func (store *Datastore) isTaken(prefix ds.Key, k string) bool {
	if store.purging.Len() == 0 {
		return false
	}
	c, err := cidOfBlockKey(prefix, k)
	return err == nil && store.purging.Has(c)
}

// dropTaken removes the blocks take returned, then their index entries, and
// returns how many it dropped. The caller does not hold store.mu: removing
// the blocks is the slow part of a purge, and the broadcast reader and every
// walk wait on the lock. Nothing reads a taken block meanwhile: it is no
// head, a walk reaching it sees it is not held, and a merge of it is refused
// until the purge ends, for repair to retry. Its entry goes after it, once
// its removal is durable, so a crash in between leaves an entry naming a
// block that is gone, which the next purge takes again, and never a block
// nothing indexes. A snapshot's state is retired with its entry.
func (store *Datastore) dropTaken(ctx context.Context, taken []cid.Cid) (int, error) {
	if len(taken) == 0 {
		return 0, nil
	}
	defer store.dropping.Done()
	err := store.dagService.RemoveMany(ctx, taken)
	if err != nil {
		err = fmt.Errorf("error removing purged blocks: %w", err)
	} else {
		err = store.syncBlocks(ctx)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, c := range taken {
		store.purging.Remove(c)
	}
	if err == nil {
		err = store.unindex(ctx, taken)
	}
	if err != nil {
		// What is still indexed counts again, and the next purge takes it.
		store.lows.block = 0
		for _, c := range taken {
			if held, herr := store.store.Has(ctx, store.blockKey(c)); herr == nil && held {
				store.blockCount++
			}
		}
		return 0, err
	}
	return len(taken), nil
}

// unindex removes the index entries of cids, retiring the state each
// snapshot among them names, with the manifest of what that leaves
// unreached, as one atomic commit. The caller holds store.mu.
func (store *Datastore) unindex(ctx context.Context, cids []cid.Cid) error {
	store.gcMu.Lock()
	defer store.gcMu.Unlock()
	t := newTxn(store.store)
	ch := store.changeRoots()
	for _, c := range cids {
		v, err := store.store.Get(ctx, store.blockKey(c))
		if errors.Is(err, ds.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		b, err := decodeBlock(v)
		if err != nil {
			return err
		}
		ch.remove(b.state)
		t.del(store.blockKey(c))
	}
	if err := ch.prepare(ctx, t); err != nil {
		return err
	}
	if err := store.commitAtomic(ctx, t); err != nil {
		return err
	}
	ch.apply()
	return nil
}
