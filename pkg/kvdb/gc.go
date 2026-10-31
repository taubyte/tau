package kvdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"time"
	"unsafe"

	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/taubyte/tau/pkg/kvdb/mst"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"github.com/taubyte/tau/pkg/pebbleds"
	"google.golang.org/protobuf/proto"
)

// A state's blocks, its StateRoot and the nodes of its trees, are shared:
// one node sits under any number of snapshots, this replica's and those it
// merged, and under the base the journal is applied to. No dot owns one, so
// the purge that drops a snapshot's block cannot drop its state. A state
// block is kept while a root reaches it. The roots are the StateRoot of
// each indexed snapshot (its index entry names it), the base's trees, and
// each state pinned for a reader (see statePin).
//
// The blocks the roots reach are the members, known in memory, and every
// one is held: a root is admitted only once its whole closure is. There
// are no counts. When roots retire, what only they reached is found by
// diffing each of their trees against every tree of the same kind a root
// still has (mst.Differ): a subtree two trees share is passed over unread,
// so the work is the paths that differ, never the children they share.
// That garbage is recorded, in chunks, as a deletion manifest committed
// with the retirement, atomically, while the retired trees can still be
// read. The manifest's blocks are then removed, an absent one counting as
// removed, and the manifest goes once the removals are durable. Admitting a
// root, pinning one or staging a block first drains every manifest, so
// nothing a manifest names is reached again before it is gone.
//
// A block is protected by its cid wherever it sits, and diffing trees of
// one kind only is enough for that: no node can sit in trees of two kinds,
// since a removal's or a context entry's key begins with two zero bytes,
// which no escaped element key can, and removal and context values differ
// in length; and every entry of a tree a merge admits was parsed as its
// kind's, or equals one of an admitted tree. A StateRoot is no tree node.
//
// A block written or fetched before a root reaches it belongs to a session,
// a compaction or a state fetch, which records it as an intent, durably, in
// chunks under the session, before it is written. The merge that publishes
// the session's state removes its intents in the commit that admits the
// root. A session that ends otherwise leaves the intents no root reaches
// and no other session holds to a manifest. A session reuses what the
// members hold instead of writing or fetching it again, and holds what it
// reuses: should the roots that reach it retire before the session's merge,
// the blocks it would lose become the session's intents, recorded with the
// retirement. After a crash no session is left, and open removes every
// intent no root reaches.
//
//	/i/<session>/<chunk>  a session's intents
//	/g/<seq>/<chunk>      a deletion manifest
//	/p/<id>               a pinned state
//	/x/<cid>              a fetched delta block not indexed yet
//
// A state block is CIDv1 raw sha2-256, which a fetch checks before it
// asks for one, so it is named by its digest, 32 bytes: the sets of them
// hold no pointers, and the records are their digests end to end.
//
// Delta blocks are kept by the index, not by roots: a walk marks what it
// fetches (/x/) and indexing a block unmarks it; reclaim removes a marked
// block nothing indexes or stages.
const (
	intentNs   = "i"
	manifestNs = "g"
	pinNs      = "p"
	reclaimNs  = "x"
	baseKey    = "sr"
	redoNs     = "w"
	redoMark   = "wr"
	appliedKey = "wa"
)

// redoChunkBytes bounds a chunk of an atomic commit's record. One value
// holding a whole commit, all of a cold catch-up's state, is decompressed by
// every lookup the store's index sends to its block, long after it is
// applied and deleted.
const redoChunkBytes = 4 << 10

// chunkDigests bounds the digests a chunk of intents or of a manifest
// holds, for the same reason.
const chunkDigests = redoChunkBytes / len(cidKey{})

// reclaimBatch bounds the blocks reclaim removes under gcMu at a time:
// every merge that changes ownership waits for it.
const reclaimBatch = 512

func (store *Datastore) reclaimPrefix() ds.Key  { return ds.RawKey(store.pre.reclaim) }
func (store *Datastore) intentPrefix() ds.Key   { return ds.RawKey(store.pre.intents) }
func (store *Datastore) manifestPrefix() ds.Key { return ds.RawKey(store.pre.manifests) }
func (store *Datastore) pinPrefix() ds.Key      { return ds.RawKey(store.pre.pins) }

func (store *Datastore) reclaimKey(c cid.Cid) ds.Key { return hashKey(store.pre.reclaim, c) }

// chunkKey is prefix/<n>/<i>, both zero-padded.
func chunkKey(prefix string, n uint64, i uint64) ds.Key {
	b := make([]byte, 0, len(prefix)+2+2*counterDigits)
	b = appendCounter(append(append(b, prefix...), '/'), n)
	return dsKey(appendCounter(append(b, '/'), i))
}

func (store *Datastore) intentKey(session, i uint64) ds.Key {
	return chunkKey(store.pre.intents, session, i)
}

func (store *Datastore) manifestKey(seq, i uint64) ds.Key {
	return chunkKey(store.pre.manifests, seq, i)
}

// parseChunkKey is the two numbers of a chunk key under prefix.
func parseChunkKey(prefix ds.Key, k string) (uint64, uint64, error) {
	rest := k[min(len(k), len(prefix.String())+1):]
	if len(rest) != 2*counterDigits+1 || rest[counterDigits] != '/' {
		return 0, 0, fmt.Errorf("bad chunk key %q", k)
	}
	var n [2]uint64
	for i, s := range []string{rest[:counterDigits], rest[counterDigits+1:]} {
		for _, d := range []byte(s) {
			if d < '0' || d > '9' {
				return 0, 0, fmt.Errorf("bad chunk key %q", k)
			}
			n[i] = n[i]*10 + uint64(d-'0')
		}
	}
	return n[0], n[1], nil
}

// isStateCid reports whether c is a cid a state block may have.
func isStateCid(c cid.Cid) bool {
	ks := c.KeyString()
	return len(ks) == len(mst.BlockPrefix)+len(cidKey{}) && ks[:len(mst.BlockPrefix)] == mst.BlockPrefix
}

// cidOf is the state block named by k.
func cidOf(k cidKey) cid.Cid {
	var b [len(mst.BlockPrefix) + len(cidKey{})]byte
	copy(b[copy(b[:], mst.BlockPrefix):], k[:])
	c, err := cid.Cast(b[:])
	if err != nil {
		panic(err)
	}
	return c
}

func digestBytes(keys []cidKey) []byte {
	if len(keys) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&keys[0])), len(keys)*len(cidKey{}))
}

func appendDigests(keys []cidKey, v []byte) ([]cidKey, error) {
	if len(v)%len(cidKey{}) != 0 {
		return keys, errors.New("bad digest list")
	}
	for i := 0; i < len(v); i += len(cidKey{}) {
		keys = append(keys, cidKey(v[i:i+len(cidKey{})]))
	}
	return keys, nil
}

func compareKeys(a, b cidKey) int { return bytes.Compare(a[:], b[:]) }

func hasSorted(keys []cidKey, k cidKey) bool {
	_, ok := slices.BinarySearchFunc(keys, k, compareKeys)
	return ok
}

// A rootKind is what a root names: a StateRoot, or the root of one of the
// base's trees.
type rootKind uint8

const (
	stateRootKind rootKind = iota
	elemsRootKind
	pendingRootKind
)

// A liveRoot is a root with the number of records naming it.
type liveRoot struct {
	c    cid.Cid
	kind rootKind
	sr   stateRoot
	n    int
}

// trees is the trees r names: elements, pending removals and context.
func (r *liveRoot) trees() [3]cid.Cid {
	switch r.kind {
	case elemsRootKind:
		return [3]cid.Cid{r.c, cid.Undef, cid.Undef}
	case pendingRootKind:
		return [3]cid.Cid{cid.Undef, r.c, cid.Undef}
	}
	return [3]cid.Cid{r.sr.elems, r.sr.pending, r.sr.ctx}
}

// A gcSession is a compaction or a state fetch: the blocks it means to
// write or fetch before a merge publishes them, its intents, and the
// members it reuses, which it holds until it ends.
type gcSession struct {
	id        uint64
	marks     map[cidKey]uint8
	intents   []cidKey
	borrowed  []cidKey
	chunks    uint64
	published bool
}

const (
	markBorrowed uint8 = iota + 1
	markIntent
)

func newSession() *gcSession { return &gcSession{marks: map[cidKey]uint8{}} }

func (s *gcSession) reset() {
	clear(s.marks)
	s.intents, s.borrowed = s.intents[:0], s.borrowed[:0]
	s.chunks, s.published = 0, false
}

// A manifest is a deletion manifest not drained yet.
type manifest struct {
	seq    uint64
	chunks uint64
}

// collector is what gcMu guards.
type collector struct {
	members  map[cidKey]struct{}
	roots    map[cidKey]*liveRoot
	staged   map[cidKey]int
	sessions []*gcSession
	// ended are sessions whose intents could not be removed when they
	// ended, for reclaim to retry.
	ended     []*gcSession
	manifests []manifest
	pins      map[uint64]*statePin

	nextSession, nextManifest, nextPin uint64

	compaction *gcSession
	// recent reads the nodes the last compaction wrote from memory: the
	// diff that retires the snapshot before it reads them next.
	recent recentDAG
	// Storage a root change reuses, one change at a time.
	change          rootChange
	differ          mst.Differ
	cand, next      []cidKey
	seen            map[cidKey]bool
	live, retired   []*liveRoot
	liveTrees, done [3][]cid.Cid
}

func newCollector(dags ipld.DAGService) collector {
	return collector{
		recent:     recentDAG{DAGService: dags},
		members:    map[cidKey]struct{}{},
		roots:      map[cidKey]*liveRoot{},
		staged:     map[cidKey]int{},
		pins:       map[uint64]*statePin{},
		compaction: newSession(),
		seen:       map[cidKey]bool{},
	}
}

func (gc *collector) member(k cidKey) bool {
	_, ok := gc.members[k]
	return ok
}

// holds reports whether a session in progress, or a walk, holds k.
func (gc *collector) holds(k cidKey) bool {
	if gc.staged[k] > 0 {
		return true
	}
	for _, s := range gc.sessions {
		if s.marks[k] == markIntent {
			return true
		}
	}
	return false
}

// begin registers s as a session in progress, with a new number. The
// caller holds gcMu.
func (store *Datastore) beginSession(s *gcSession) {
	s.reset()
	s.id = store.gc.nextSession
	store.gc.nextSession++
	store.gc.sessions = append(store.gc.sessions, s)
}

func (gc *collector) unregister(s *gcSession) {
	if i := slices.Index(gc.sessions, s); i >= 0 {
		gc.sessions = slices.Delete(gc.sessions, i, i+1)
	}
}

// claim takes the blocks of cids s does not hold yet: a member is
// borrowed, any other becomes an intent, appended to fresh. The caller
// holds gcMu and records the new intents before it writes or fetches them.
func (s *gcSession) claim(gc *collector, c cid.Cid, fresh []cid.Cid) []cid.Cid {
	k := keyOf(c)
	if _, ok := s.marks[k]; ok {
		return fresh
	}
	if gc.member(k) {
		s.marks[k] = markBorrowed
		s.borrowed = append(s.borrowed, k)
		return fresh
	}
	s.marks[k] = markIntent
	s.intents = append(s.intents, k)
	return append(fresh, c)
}

// unclaim forgets the last n intents s claimed, which it could not record.
// The caller holds gcMu.
func (s *gcSession) unclaim(n int) {
	for _, k := range s.intents[len(s.intents)-n:] {
		delete(s.marks, k)
	}
	s.intents = s.intents[:len(s.intents)-n]
}

// recordIntents durably records s's last n intents, in chunks numbered
// from first, before what they name is written or fetched.
func (store *Datastore) recordIntents(ctx context.Context, s *gcSession, first uint64, keys []cidKey) error {
	for i := 0; len(keys) > 0; i++ {
		n := min(len(keys), chunkDigests)
		k := store.intentKey(s.id, first+uint64(i))
		if err := store.raw.Put(ctx, k, bytes.Clone(digestBytes(keys[:n]))); err != nil {
			return err
		}
		if err := store.raw.Sync(ctx, k); err != nil {
			return err
		}
		keys = keys[n:]
	}
	return nil
}

func chunksFor(n int) uint64 { return uint64((n + chunkDigests - 1) / chunkDigests) }

// intend claims cids for s and records the intents that are new. The
// returned blocks are the ones to write or fetch; a failure leaves s as it
// was.
func (store *Datastore) intend(ctx context.Context, s *gcSession, cids []cid.Cid, fresh []cid.Cid) ([]cid.Cid, error) {
	store.gcMu.Lock()
	if err := store.drainLocked(ctx); err != nil {
		store.gcMu.Unlock()
		return fresh, err
	}
	for _, c := range cids {
		if !isStateCid(c) {
			store.gcMu.Unlock()
			return fresh, fmt.Errorf("state block %s: %w", c, errMalformedState)
		}
	}
	from := len(fresh)
	for _, c := range cids {
		fresh = s.claim(&store.gc, c, fresh)
	}
	n := len(fresh) - from
	first := s.chunks
	s.chunks += chunksFor(n)
	keys := s.intents[len(s.intents)-n:]
	store.gcMu.Unlock()
	if err := store.recordIntents(ctx, s, first, keys); err != nil {
		store.gcMu.Lock()
		s.unclaim(n)
		store.gcMu.Unlock()
		return fresh[:from], err
	}
	return fresh, nil
}

// endSession ends s: unless a merge published its state, what it fetched
// or wrote that no root reaches and no other session holds goes, through a
// manifest recorded before its intents go.
func (store *Datastore) endSession(ctx context.Context, s *gcSession) error {
	store.gcMu.Lock()
	defer store.gcMu.Unlock()
	store.gc.unregister(s)
	if s.published || s.chunks == 0 {
		return nil
	}
	if err := store.dropIntentsLocked(ctx, s.id, s.chunks, s.intents); err != nil {
		store.gc.ended = append(store.gc.ended, &gcSession{id: s.id, chunks: s.chunks, intents: slices.Clone(s.intents)})
		return err
	}
	return store.drainLocked(ctx)
}

// dropIntentsLocked turns the intents of an ended session into a manifest
// of those no root reaches and no session holds, then removes them. The
// caller holds gcMu.
func (store *Datastore) dropIntentsLocked(ctx context.Context, session, chunks uint64, intents []cidKey) error {
	gc := &store.gc
	gc.cand = gc.cand[:0]
	for _, k := range intents {
		if !gc.member(k) && !gc.holds(k) {
			gc.cand = append(gc.cand, k)
		}
	}
	if len(gc.cand) > 0 {
		t := newTxn(store.raw)
		m := store.recordManifest(t, gc.cand)
		if err := t.commit(ctx); err != nil {
			return err
		}
		if err := store.raw.Sync(ctx, store.manifestPrefix()); err != nil {
			return err
		}
		gc.manifests = append(gc.manifests, m)
	}
	t := newTxn(store.raw)
	for i := range chunks {
		t.del(store.intentKey(session, i))
	}
	return t.commit(ctx)
}

// recordManifest stages the manifest of keys in t.
func (store *Datastore) recordManifest(t *txn, keys []cidKey) manifest {
	m := manifest{seq: store.gc.nextManifest}
	store.gc.nextManifest++
	for len(keys) > 0 {
		n := min(len(keys), chunkDigests)
		t.put(store.manifestKey(m.seq, m.chunks), bytes.Clone(digestBytes(keys[:n])))
		m.chunks++
		keys = keys[n:]
	}
	return m
}

// drainLocked removes the blocks of every manifest, then the manifests,
// durably: one a crash brought back could otherwise remove a block a root
// reaches again since. A block a root or a session holds again is passed
// over all the same. The caller holds gcMu.
func (store *Datastore) drainLocked(ctx context.Context) error {
	gc := &store.gc
	var cids []cid.Cid
	for len(gc.manifests) > 0 {
		m := gc.manifests[0]
		for i := range m.chunks {
			v, err := store.raw.Get(ctx, store.manifestKey(m.seq, i))
			if errors.Is(err, ds.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if gc.next, err = appendDigests(gc.next[:0], v); err != nil {
				return fmt.Errorf("manifest %d: %w", m.seq, err)
			}
			for _, k := range gc.next {
				if gc.member(k) || gc.holds(k) {
					continue
				}
				cids = append(cids, cidOf(k))
				if len(cids) == reclaimBatch {
					if err := store.removeBlocks(ctx, cids); err != nil {
						return fmt.Errorf("error removing unreached state blocks: %w", err)
					}
					cids = cids[:0]
				}
			}
		}
		if len(cids) > 0 {
			if err := store.removeBlocks(ctx, cids); err != nil {
				return fmt.Errorf("error removing unreached state blocks: %w", err)
			}
			cids = cids[:0]
		}
		if err := store.syncBlocks(ctx); err != nil {
			return err
		}
		t := newTxn(store.raw)
		for i := range m.chunks {
			t.del(store.manifestKey(m.seq, i))
		}
		if err := t.commit(ctx); err != nil {
			return err
		}
		if err := store.raw.Sync(ctx, store.manifestPrefix()); err != nil {
			return err
		}
		gc.manifests = gc.manifests[1:]
	}
	return nil
}

// A BlockSyncer is a DAGService that keeps the blocks it writes in memory
// until SyncBlocks makes them durable. One that is not one is taken to keep
// its blocks in the datastore the store is given, as the node's does, so
// that syncing that datastore syncs them.
type BlockSyncer interface {
	SyncBlocks(ctx context.Context) error
}

// syncBlocks makes durable every block written so far, and first what the
// store wrote before them: the index entry written before a block it
// names is never lost while the block is kept.
func (store *Datastore) syncBlocks(ctx context.Context) error {
	s, ok := store.dagService.(BlockSyncer)
	if !ok {
		return store.raw.Sync(ctx, ds.NewKey("/"))
	}
	if err := store.raw.Sync(ctx, store.namespace); err != nil {
		return err
	}
	return s.SyncBlocks(ctx)
}

// A rootChange is a change of the roots, planned under gcMu and applied
// once the commit that records it has.
type rootChange struct {
	store  *Datastore
	deltas []rootDelta
	plan   gcPlan
}

type rootDelta struct {
	k cidKey
	r liveRoot
	d int
}

// A gcPlan is what a root change leaves unreached: gone, sorted, which
// leaves the members; rescued, the blocks of gone that sessions reuse,
// which become their intents; and drop, the rest of gone that no session
// holds, the manifest.
type gcPlan struct {
	gone, drop []cidKey
	rescued    []rescue
	manifest   manifest
}

type rescue struct {
	s     *gcSession
	keys  []cidKey
	first uint64
}

// changeRoots begins a change of the roots, in the collector's storage:
// one is planned and applied under gcMu before the next begins.
func (store *Datastore) changeRoots() *rootChange {
	ch := &store.gc.change
	p := &ch.plan
	ch.store, ch.deltas = store, ch.deltas[:0]
	p.gone, p.drop, p.rescued, p.manifest = p.gone[:0], p.drop[:0], p.rescued[:0], manifest{}
	return ch
}

// add counts one more record naming the StateRoot sr, at c.
func (ch *rootChange) addState(c cid.Cid, sr stateRoot) {
	if c.Defined() {
		ch.deltas = append(ch.deltas, rootDelta{k: keyOf(c), r: liveRoot{c: c, kind: stateRootKind, sr: sr}, d: 1})
	}
}

func (ch *rootChange) add(c cid.Cid, kind rootKind) {
	if c.Defined() {
		ch.deltas = append(ch.deltas, rootDelta{k: keyOf(c), r: liveRoot{c: c, kind: kind}, d: 1})
	}
}

// retireAll retires every root.
func (ch *rootChange) retireAll() {
	for k, r := range ch.store.gc.roots {
		ch.deltas = append(ch.deltas, rootDelta{k: k, d: -r.n})
	}
}

func (ch *rootChange) remove(c cid.Cid) {
	if c.Defined() {
		ch.deltas = append(ch.deltas, rootDelta{k: keyOf(c), d: -1})
	}
}

// count is how many records will name k.
func (ch *rootChange) count(k cidKey) int {
	n := 0
	if r := ch.store.gc.roots[k]; r != nil {
		n = r.n
	}
	for _, d := range ch.deltas {
		if d.k == k {
			n += d.d
		}
	}
	return n
}

// root is the root k, as it is or as the change adds it.
func (ch *rootChange) root(k cidKey) *liveRoot {
	if r := ch.store.gc.roots[k]; r != nil {
		return r
	}
	for i := range ch.deltas {
		if d := &ch.deltas[i]; d.k == k && d.d > 0 {
			return &d.r
		}
	}
	return nil
}

// live is every root the change leaves, retired every one it retires.
func (ch *rootChange) roots() (live, retired []*liveRoot, err error) {
	gc := &ch.store.gc
	seen := gc.seen
	clear(seen)
	live, retired = gc.live[:0], gc.retired[:0]
	defer func() { gc.live, gc.retired = live[:0], retired[:0] }()
	visit := func(k cidKey) error {
		if seen[k] {
			return nil
		}
		seen[k] = true
		r := ch.root(k)
		if r == nil {
			return fmt.Errorf("releasing root %s, which nothing names", cidOf(k))
		}
		switch n := ch.count(k); {
		case n < 0:
			return fmt.Errorf("releasing root %s more times than it is named", r.c)
		case n > 0:
			live = append(live, r)
		case ch.store.gc.roots[k] != nil:
			retired = append(retired, r)
		}
		return nil
	}
	for k := range ch.store.gc.roots {
		if err := visit(k); err != nil {
			return nil, nil, err
		}
	}
	for _, d := range ch.deltas {
		if err := visit(d.k); err != nil {
			return nil, nil, err
		}
	}
	return live, retired, nil
}

// prepare plans the change and stages its records in t: the manifest of
// what it leaves unreached, and the intents of the sessions it rescues.
// The caller holds gcMu.
func (ch *rootChange) prepare(ctx context.Context, t *txn) error {
	store := ch.store
	gc := &store.gc
	live, retired, err := ch.roots()
	if err != nil {
		return err
	}
	p := &ch.plan
	if len(retired) == 0 {
		return nil
	}
	if len(live) == 0 {
		// Nothing is left to reach anything: every member goes.
		for k := range gc.members {
			p.gone = append(p.gone, k)
		}
		slices.SortFunc(p.gone, compareKeys)
		return ch.finish(ctx, t)
	}
	liveTrees, done := &gc.liveTrees, &gc.done
	for i := range liveTrees {
		liveTrees[i], done[i] = liveTrees[i][:0], done[i][:0]
	}
	for _, r := range live {
		for kind, c := range r.trees() {
			if c.Defined() && !slices.Contains(liveTrees[kind], c) {
				liveTrees[kind] = append(liveTrees[kind], c)
			}
		}
	}
	for _, r := range retired {
		if r.kind == stateRootKind {
			p.gone = append(p.gone, keyOf(r.c))
		}
		for kind, c := range r.trees() {
			if !c.Defined() || slices.Contains(liveTrees[kind], c) || slices.Contains(done[kind], c) {
				continue
			}
			done[kind] = append(done[kind], c)
			only, err := store.onlyIn(ctx, c, liveTrees[kind])
			if err != nil {
				return err
			}
			p.gone = append(p.gone, only...)
		}
	}
	slices.SortFunc(p.gone, compareKeys)
	p.gone = slices.Compact(p.gone)
	return ch.finish(ctx, t)
}

// finish rescues what sessions reuse from the plan's gone, and stages the
// manifest of the rest and the rescued intents in t.
func (ch *rootChange) finish(ctx context.Context, t *txn) error {
	store := ch.store
	gc := &store.gc
	p := &ch.plan
	if err := ch.rescue(ctx); err != nil {
		return err
	}
	for _, k := range p.gone {
		if !gc.holds(k) && !ch.rescued(k) {
			p.drop = append(p.drop, k)
		}
	}
	if len(p.drop) > 0 {
		p.manifest = store.recordManifest(t, p.drop)
	}
	for i := range p.rescued {
		rs := &p.rescued[i]
		rs.first = rs.s.chunks
		for j, keys := uint64(0), rs.keys; len(keys) > 0; j++ {
			n := min(len(keys), chunkDigests)
			t.put(store.intentKey(rs.s.id, rs.first+j), bytes.Clone(digestBytes(keys[:n])))
			keys = keys[n:]
		}
	}
	return nil
}

// onlyIn is the nodes of the tree c that none of trees has. The caller
// holds gcMu.
func (store *Datastore) onlyIn(ctx context.Context, c cid.Cid, trees []cid.Cid) ([]cidKey, error) {
	gc := &store.gc
	a := store.tree(&gc.recent, c)
	gc.cand = gc.cand[:0]
	if len(trees) == 0 {
		trees = noTrees
	}
	for i, l := range trees {
		onlyA, _, err := gc.differ.Nodes(ctx, a, store.tree(&gc.recent, l))
		if err != nil {
			return nil, err
		}
		gc.next = gc.next[:0]
		for _, n := range onlyA {
			gc.next = append(gc.next, keyOf(n))
		}
		slices.SortFunc(gc.next, compareKeys)
		if i == 0 {
			gc.cand = append(gc.cand, gc.next...)
		} else {
			gc.cand = intersect(gc.cand, gc.next)
		}
		if len(gc.cand) == 0 {
			break
		}
	}
	return gc.cand, nil
}

// A recentDAG is the store's DAG with the nodes the last compaction wrote
// held in memory.
type recentDAG struct {
	ipld.DAGService
	nodes map[cid.Cid]ipld.Node
}

func (r *recentDAG) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	if nd, ok := r.nodes[c]; ok {
		return nd, nil
	}
	return r.DAGService.Get(ctx, c)
}

func (r *recentDAG) ReadBlock(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	if nd, ok := r.nodes[c]; ok {
		return nd, nil
	}
	return readBlock(ctx, r.DAGService, c)
}

// openReader reads what open needs of the roots: every block is held, and
// one that is not fails in DAGSyncerTimeout rather than waits for a peer.
func (store *Datastore) openReader() ipld.NodeGetter {
	return boundedGetter{store.dagService, store.opts.DAGSyncerTimeout}
}

type boundedGetter struct {
	ipld.NodeGetter
	timeout time.Duration
}

func (g boundedGetter) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	if g.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.timeout)
		defer cancel()
	}
	return g.NodeGetter.Get(ctx, c)
}

// noTrees is a tree with nothing in it, to diff against.
var noTrees = []cid.Cid{cid.Undef}

// intersect keeps the keys of a, sorted, that sorted b also has.
func intersect(a, b []cidKey) []cidKey {
	out, j := a[:0], 0
	for _, k := range a {
		for j < len(b) && compareKeys(b[j], k) < 0 {
			j++
		}
		if j < len(b) && b[j] == k {
			out = append(out, k)
		}
	}
	return out
}

// rescue finds, for each session in progress, the blocks of the plan's
// gone under what it borrowed: they become its intents. A borrowed block
// that stays a member holds its whole closure; one that does not is walked
// down to the members.
func (ch *rootChange) rescue(ctx context.Context) error {
	store := ch.store
	gc := &store.gc
	p := &ch.plan
	stays := func(k cidKey) bool { return gc.member(k) && !hasSorted(p.gone, k) }
	for _, s := range gc.sessions {
		var keys []cidKey
		var stack []cidKey
		for _, b := range s.borrowed {
			if !stays(b) {
				stack = append(stack, b)
			}
		}
		for len(stack) > 0 {
			k := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if hasSorted(p.gone, k) && s.marks[k] != markIntent && !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
			nd, err := gc.recent.Get(ctx, cidOf(k))
			if err != nil {
				return fmt.Errorf("reading reused state block %s: %w", cidOf(k), err)
			}
			links, err := blockLinks(nd, nil)
			if err != nil {
				return fmt.Errorf("reading reused state block %s: %w", cidOf(k), err)
			}
			for _, l := range links {
				if lk := keyOf(l); !stays(lk) {
					stack = append(stack, lk)
				}
			}
		}
		if len(keys) > 0 {
			p.rescued = append(p.rescued, rescue{s: s, keys: keys})
		}
	}
	return nil
}

func (ch *rootChange) rescued(k cidKey) bool {
	for _, rs := range ch.plan.rescued {
		if slices.Contains(rs.keys, k) {
			return true
		}
	}
	return false
}

// apply makes the change, once its commit has: the roots, the members,
// the rescued intents, and the manifest to drain. The caller holds gcMu.
func (ch *rootChange) apply() {
	gc := &ch.store.gc
	for _, d := range ch.deltas {
		r := gc.roots[d.k]
		if r == nil {
			r = &liveRoot{c: d.r.c, kind: d.r.kind, sr: d.r.sr}
			gc.roots[d.k] = r
		}
		if r.n += d.d; r.n == 0 {
			delete(gc.roots, d.k)
		}
	}
	for _, k := range ch.plan.gone {
		delete(gc.members, k)
	}
	for _, rs := range ch.plan.rescued {
		for _, k := range rs.keys {
			rs.s.marks[k] = markIntent
			rs.s.intents = append(rs.s.intents, k)
		}
		rs.s.chunks = rs.first + chunksFor(len(rs.keys))
	}
	if ch.plan.manifest.chunks > 0 {
		gc.manifests = append(gc.manifests, ch.plan.manifest)
	}
}

// publish admits the change's new StateRoot, whose closure the members and
// s's intents hold, and with it s's intents, removed in t. The caller holds
// gcMu.
func (ch *rootChange) publish(s *gcSession, t *txn) {
	for i := range s.chunks {
		t.del(ch.store.intentKey(s.id, i))
	}
}

// published makes s's intents members, once the commit that admits its
// root has. The caller holds gcMu.
func (store *Datastore) published(s *gcSession) {
	for _, k := range s.intents {
		store.gc.members[k] = struct{}{}
	}
	s.published = true
}

// reaches reports whether the members or s's intents hold c.
func (gc *collector) reaches(s *gcSession, c cid.Cid) bool {
	k := keyOf(c)
	return gc.member(k) || s != nil && s.marks[k] == markIntent
}

// stage keeps cids, delta blocks a walk fetches, from reclaim until
// unstage. With mark, each one is also marked for removal, durably, before
// the caller fetches it.
func (store *Datastore) stage(ctx context.Context, cids []cid.Cid, mark bool) error {
	store.gcMu.Lock()
	defer store.gcMu.Unlock()
	for _, c := range cids {
		store.gc.staged[keyOf(c)]++
	}
	if !mark {
		return nil
	}
	t := newTxn(store.store)
	for _, c := range cids {
		t.put(store.reclaimKey(c), nil)
	}
	err := t.commit(ctx)
	t.release()
	if err != nil {
		return err
	}
	return store.raw.Sync(ctx, store.reclaimPrefix())
}

func (store *Datastore) unstage(cids []cid.Cid) {
	store.gcMu.Lock()
	defer store.gcMu.Unlock()
	for _, c := range cids {
		k := keyOf(c)
		if store.gc.staged[k]--; store.gc.staged[k] <= 0 {
			delete(store.gc.staged, k)
		}
	}
}

// reclaim drains every manifest, releases the pins that have expired,
// retries the sessions whose intents could not be removed, and removes
// every marked delta block that nothing indexes or stages. It returns how
// many delta blocks it removed.
func (store *Datastore) reclaim(ctx context.Context) (int, error) {
	if err := store.reclaimState(ctx); err != nil {
		return 0, err
	}
	total := 0
	for {
		n, more, err := store.reclaimSome(ctx)
		total += n
		if err != nil || !more {
			return total, err
		}
	}
}

func (store *Datastore) reclaimState(ctx context.Context) error {
	store.gcMu.Lock()
	defer store.gcMu.Unlock()
	if err := store.releaseExpiredLocked(ctx); err != nil {
		return err
	}
	for len(store.gc.ended) > 0 {
		s := store.gc.ended[0]
		if err := store.dropIntentsLocked(ctx, s.id, s.chunks, s.intents); err != nil {
			return err
		}
		store.gc.ended = store.gc.ended[1:]
	}
	return store.drainLocked(ctx)
}

func (store *Datastore) reclaimSome(ctx context.Context) (int, bool, error) {
	store.gcMu.Lock()
	defer store.gcMu.Unlock()
	prefix := store.reclaimPrefix()
	var drop []cid.Cid
	t := newTxn(store.store)
	more := false
	err := scanStore(ctx, store.store, prefix, func(k string, _ []byte) error {
		c, err := cidOfBlockKey(prefix, k)
		if err != nil {
			return err
		}
		if store.gc.staged[keyOf(c)] > 0 {
			return nil
		}
		if len(drop) == reclaimBatch {
			more = true
			return nil
		}
		indexed, err := store.isIndexed(ctx, c)
		if err != nil {
			return err
		}
		if !indexed {
			drop = append(drop, c)
		}
		t.del(ds.RawKey(k))
		return nil
	})
	if err != nil || len(t.order) == 0 {
		return 0, false, err
	}
	if len(drop) > 0 {
		if err := store.removeBlocks(ctx, drop); err != nil {
			return 0, false, fmt.Errorf("error removing unindexed blocks: %w", err)
		}
	}
	// A mark goes once what it found is durable: the entry of a block
	// indexed, the removal of one that is not.
	if err := store.syncBlocks(ctx); err != nil {
		return 0, false, err
	}
	return len(drop), more, t.commit(ctx)
}

// openGC loads the roots and their members, drains the manifests a crash
// left, and ends what a crash interrupted: the pins of the process that
// took them and the sessions in progress. Nothing else runs yet.
func (store *Datastore) openGC(ctx context.Context) error {
	store.gcMu.Lock()
	defer store.gcMu.Unlock()
	gc := &store.gc
	ch := store.changeRoots()
	prefix := store.blocksPrefix()
	err := scanStore(ctx, store.store, prefix, func(_ string, v []byte) error {
		b, err := decodeBlock(v)
		if err != nil || !b.state.Defined() {
			return err
		}
		r := gc.roots[keyOf(b.state)]
		if r == nil {
			sr, err := loadStateRoot(ctx, store.openReader(), b.state)
			if err != nil {
				return fmt.Errorf("error reading the state of an indexed snapshot: %w", err)
			}
			r = &liveRoot{c: b.state, kind: stateRootKind, sr: sr}
			gc.roots[keyOf(b.state)] = r
		}
		r.n++
		return nil
	})
	if err != nil {
		return err
	}
	for _, br := range []struct {
		c    cid.Cid
		kind rootKind
	}{{store.base.elems, elemsRootKind}, {store.base.pending, pendingRootKind}} {
		if br.c.Defined() {
			r := gc.roots[keyOf(br.c)]
			if r == nil {
				r = &liveRoot{c: br.c, kind: br.kind}
				gc.roots[keyOf(br.c)] = r
			}
			r.n++
		}
	}
	var pins []string
	if err := scanStore(ctx, store.store, store.pinPrefix(), func(k string, v []byte) error {
		c, err := cid.Cast(v)
		if err != nil {
			return fmt.Errorf("pin %s: %w", k, err)
		}
		r := gc.roots[keyOf(c)]
		if r == nil {
			sr, err := loadStateRoot(ctx, store.openReader(), c)
			if err != nil {
				return fmt.Errorf("error reading a pinned state: %w", err)
			}
			r = &liveRoot{c: c, kind: stateRootKind, sr: sr}
			gc.roots[keyOf(c)] = r
		}
		r.n++
		ch.remove(c)
		pins = append(pins, k)
		return nil
	}); err != nil {
		return err
	}
	for _, r := range gc.roots {
		if err := store.markClosure(ctx, r.c); err != nil {
			return err
		}
	}

	if err := scanStore(ctx, store.store, store.manifestPrefix(), func(k string, _ []byte) error {
		seq, i, err := parseChunkKey(store.manifestPrefix(), k)
		if err != nil {
			return err
		}
		if n := len(gc.manifests); n > 0 && gc.manifests[n-1].seq == seq {
			gc.manifests[n-1].chunks = max(gc.manifests[n-1].chunks, i+1)
		} else {
			gc.manifests = append(gc.manifests, manifest{seq: seq, chunks: i + 1})
		}
		gc.nextManifest = seq + 1
		return nil
	}); err != nil {
		return err
	}
	if err := store.drainLocked(ctx); err != nil {
		return err
	}

	if len(pins) > 0 {
		t := newTxn(store.store)
		for _, k := range pins {
			t.del(ds.RawKey(k))
		}
		if err := ch.prepare(ctx, t); err != nil {
			return err
		}
		if err := store.commitAtomic(ctx, t); err != nil {
			return err
		}
		ch.apply()
		if err := store.drainLocked(ctx); err != nil {
			return err
		}
	}

	var intents []cidKey
	var sessions []uint64
	var last uint64
	if err := scanStore(ctx, store.store, store.intentPrefix(), func(k string, v []byte) error {
		s, _, err := parseChunkKey(store.intentPrefix(), k)
		if err != nil {
			return err
		}
		if len(sessions) == 0 || s != sessions[len(sessions)-1] {
			sessions = append(sessions, s)
		}
		last = max(last, s)
		intents, err = appendDigests(intents, v)
		return err
	}); err != nil {
		return err
	}
	if len(sessions) > 0 {
		gc.nextSession = last + 1
		gc.cand = gc.cand[:0]
		for _, k := range intents {
			if !gc.member(k) {
				gc.cand = append(gc.cand, k)
			}
		}
		if len(gc.cand) > 0 {
			t := newTxn(store.raw)
			m := store.recordManifest(t, gc.cand)
			if err := t.commit(ctx); err != nil {
				return err
			}
			if err := store.raw.Sync(ctx, store.manifestPrefix()); err != nil {
				return err
			}
			gc.manifests = append(gc.manifests, m)
		}
		t := newTxn(store.store)
		if err := scanStore(ctx, store.store, store.intentPrefix(), func(k string, _ []byte) error {
			t.del(ds.RawKey(k))
			return nil
		}); err != nil {
			return err
		}
		if err := t.commit(ctx); err != nil {
			return err
		}
	}
	return store.drainLocked(ctx)
}

// markClosure makes every block under c a member. The caller holds gcMu.
func (store *Datastore) markClosure(ctx context.Context, c cid.Cid) error {
	ng := store.openReader()
	stack := []cid.Cid{c}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		k := keyOf(c)
		if store.gc.member(k) {
			continue
		}
		nd, err := ng.Get(ctx, c)
		if err != nil {
			return fmt.Errorf("reading state block %s: %w", c, err)
		}
		store.gc.members[k] = struct{}{}
		if stack, err = blockLinks(nd, stack); err != nil {
			return fmt.Errorf("reading state block %s: %w", c, err)
		}
	}
	return nil
}

// commitAtomic commits t so that a crash at any point leaves all of its
// writes or none, whatever batches the store has: they are recorded first,
// in chunks under their sequence number, then a mark names the number and
// the chunk count, then they are applied in one batch, in no particular
// order, and only then is that number recorded as applied; open applies a
// marked record that is not. A batch that fails is given up the same way,
// so on a store whose batches are atomic an error leaves none of the
// writes. It writes past the journaling store, recording the journal's
// marks with the rest.
func (store *Datastore) commitAtomic(ctx context.Context, t *txn) error {
	if len(t.writes) == 0 {
		return nil
	}
	seq, err := readMillis(ctx, store.raw, store.appliedKey())
	if err != nil {
		return err
	}
	seq++
	// The writes are the txn's keys and values, lent, and its marks, built
	// in storage the store keeps; the record is made of them, then applied.
	r := &store.redo
	r.start(3 * len(t.order))
	defer r.done()
	var chunks []*pb.Redo
	cur, size := &pb.Redo{Seq: seq}, 0
	add := func(key, value []byte, del bool) {
		r.writes = append(r.writes, pb.Write{Key: key, Value: value, Delete: del})
		w := &r.writes[len(r.writes)-1]
		n := w.SizeVT()
		if size > 0 && size+n > redoChunkBytes {
			chunks = append(chunks, cur)
			cur, size = &pb.Redo{Seq: seq}, 0
		}
		cur.Writes = append(cur.Writes, w)
		size += n
	}
	view := func(k []byte, fn func(v []byte) error) error { return store.view(ctx, k, fn) }
	mark := func(m []byte) error {
		start := len(r.keys)
		r.keys = append(r.keys, m...)
		add(r.keys[start:len(r.keys):len(r.keys)], nil, false)
		return nil
	}
	for _, k := range t.order {
		v := t.writes[k]
		if _, err := store.journal.appendMarks(&r.look, k, v, view, mark); err != nil {
			return err
		}
		add(bytesOf(k), v, v == nil)
	}
	chunks = append(chunks, cur)
	err = store.recordRedo(ctx, seq, chunks)
	if err == nil {
		err = store.applyRedo(ctx, seq, chunks)
	}
	if err != nil {
		return errors.Join(err, store.raw.Put(ctx, store.appliedKey(), binary.AppendUvarint(nil, seq)))
	}
	// The commit is applied whatever happens to its record now: what is
	// left of it, open removes.
	if err := store.dropChunks(ctx, seq, len(chunks)); err != nil {
		store.logger.Errorf("error removing the record of commit %d: %s", seq, err)
	}
	return nil
}

func (store *Datastore) dropChunks(ctx context.Context, seq uint64, n int) error {
	b, err := batchOf(ctx, store.raw)
	if err != nil {
		return err
	}
	for i := range n {
		if err := b.Delete(ctx, store.redoChunkKey(seq, i)); err != nil {
			return err
		}
	}
	return b.Commit(ctx)
}

func (store *Datastore) appliedKey() ds.Key  { return store.namespace.ChildString(appliedKey) }
func (store *Datastore) redoMarkKey() ds.Key { return store.namespace.ChildString(redoMark) }
func (store *Datastore) redoPrefix() ds.Key  { return ds.RawKey(store.pre.redo) }

func (store *Datastore) redoChunkKey(seq uint64, i int) ds.Key {
	return chunkKey(store.pre.redo, seq, uint64(i))
}

// recordRedo writes the chunks of record seq durably, then the mark that
// makes it whole.
func (store *Datastore) recordRedo(ctx context.Context, seq uint64, chunks []*pb.Redo) error {
	b, err := batchOf(ctx, store.raw)
	if err != nil {
		return err
	}
	for i, c := range chunks {
		data, err := c.MarshalVT()
		if err != nil {
			return err
		}
		if err := b.Put(ctx, store.redoChunkKey(seq, i), data); err != nil {
			return err
		}
	}
	if err := b.Commit(ctx); err != nil {
		return err
	}
	if err := store.raw.Sync(ctx, store.redoPrefix()); err != nil {
		return err
	}
	mark := binary.AppendUvarint(binary.AppendUvarint(nil, seq), uint64(len(chunks)))
	if err := store.raw.Put(ctx, store.redoMarkKey(), mark); err != nil {
		return err
	}
	return store.raw.Sync(ctx, store.redoMarkKey())
}

func (store *Datastore) applyRedo(ctx context.Context, seq uint64, chunks []*pb.Redo) error {
	b, err := batchOf(ctx, store.raw)
	if err != nil {
		return err
	}
	// A pebbleds store copies what its batch is given; another may keep it,
	// and a write's key and value are a txn's, used again once it is done.
	_, copies := store.raw.(*pebbleds.Store)
	for _, c := range chunks {
		for _, w := range c.GetWrites() {
			k := stringOf(w.GetKey())
			if !copies {
				k = string(w.GetKey())
			}
			if w.GetDelete() {
				err = b.Delete(ctx, ds.RawKey(k))
			} else {
				v := w.GetValue()
				switch {
				case v == nil:
					v = []byte{}
				case !copies:
					v = bytes.Clone(v)
				}
				err = b.Put(ctx, ds.RawKey(k), v)
			}
			if err != nil {
				return err
			}
		}
	}
	if err := b.Commit(ctx); err != nil {
		return err
	}
	// The writes are durable before the number that says they are applied,
	// and that number before anything removes the record: a store that
	// keeps writes it was not asked to sync in no particular order could
	// otherwise keep the number and lose writes, or lose the number and keep
	// the removal of the record that would have applied them.
	if err := store.raw.Sync(ctx, store.namespace); err != nil {
		return err
	}
	if err := store.raw.Put(ctx, store.appliedKey(), binary.AppendUvarint(nil, seq)); err != nil {
		return err
	}
	return store.raw.Sync(ctx, store.appliedKey())
}

// replay applies the atomic commit a crash interrupted, if any, and removes
// what is left of every record.
func (store *Datastore) replay(ctx context.Context) error {
	mark, err := store.raw.Get(ctx, store.redoMarkKey())
	if err != nil && !errors.Is(err, ds.ErrNotFound) {
		return err
	}
	if err == nil {
		seq, n1 := binary.Uvarint(mark)
		count, n2 := uint64(0), 0
		if n1 > 0 {
			count, n2 = binary.Uvarint(mark[n1:])
		}
		if n1 <= 0 || n2 <= 0 {
			return fmt.Errorf("error decoding the interrupted commit's mark")
		}
		applied, err := readMillis(ctx, store.raw, store.appliedKey())
		if err != nil {
			return err
		}
		if seq > applied {
			chunks := make([]*pb.Redo, count)
			for i := range chunks {
				data, err := store.raw.Get(ctx, store.redoChunkKey(seq, i))
				if err != nil {
					return fmt.Errorf("error reading the interrupted commit: %w", err)
				}
				chunks[i] = &pb.Redo{}
				if err := proto.Unmarshal(data, chunks[i]); err != nil {
					return fmt.Errorf("error reading the interrupted commit: %w", err)
				}
				if chunks[i].GetSeq() != seq {
					return fmt.Errorf("interrupted commit %d holds a chunk of commit %d", seq, chunks[i].GetSeq())
				}
			}
			if err := store.applyRedo(ctx, seq, chunks); err != nil {
				return err
			}
		}
	}
	b, err := batchOf(ctx, store.raw)
	if err != nil {
		return err
	}
	if err := scanStore(ctx, store.raw, store.redoPrefix(), func(k string, _ []byte) error {
		return b.Delete(ctx, ds.RawKey(k))
	}); err != nil {
		return err
	}
	return b.Commit(ctx)
}

// A stateBase is the trees the journal is applied to: the state's elements
// and pending removals as they were when the journal was last emptied.
type stateBase struct {
	elems, pending cid.Cid
}

func (store *Datastore) baseKey() ds.Key { return store.namespace.ChildString(baseKey) }

func encodeBase(b stateBase) []byte {
	var out []byte
	for _, c := range []cid.Cid{b.elems, b.pending} {
		var cb []byte
		if c.Defined() {
			cb = c.Bytes()
		}
		out = append(binary.AppendUvarint(out, uint64(len(cb))), cb...)
	}
	return out
}

func decodeBase(v []byte) (stateBase, error) {
	var cs [2]cid.Cid
	for i := range cs {
		n, k := binary.Uvarint(v)
		if k <= 0 || uint64(len(v)-k) < n {
			return stateBase{}, errors.New("error decoding the state base")
		}
		if n > 0 {
			c, err := cid.Cast(v[k : k+int(n)])
			if err != nil {
				return stateBase{}, err
			}
			cs[i] = c
		}
		v = v[k+int(n):]
	}
	if len(v) != 0 {
		return stateBase{}, errors.New("error decoding the state base")
	}
	return stateBase{elems: cs[0], pending: cs[1]}, nil
}

func (store *Datastore) loadBase(ctx context.Context) (stateBase, error) {
	v, err := store.store.Get(ctx, store.baseKey())
	if errors.Is(err, ds.ErrNotFound) {
		return stateBase{}, nil
	}
	if err != nil {
		return stateBase{}, err
	}
	return decodeBase(v)
}

// setBase stages a move of the base to b in t, its trees counted as roots
// in ch.
func (ch *rootChange) setBase(t *txn, from, to stateBase) {
	if from == to {
		return
	}
	ch.add(to.elems, elemsRootKind)
	ch.add(to.pending, pendingRootKind)
	ch.remove(from.elems)
	ch.remove(from.pending)
	t.put(ch.store.baseKey(), encodeBase(to))
}

// redoScratch is what commitAtomic uses again from one commit to the next:
// the writes of its record, the keys of its marks, and scratch for a mark
// being built.
type redoScratch struct {
	writes     []pb.Write
	keys, look []byte
}

// keptRedo bounds the writes a store keeps room for between commits.
const keptRedo = 1 << 12

// start readies r for a commit of at most n writes: the record points into
// writes, which must not move while it is made.
func (r *redoScratch) start(n int) {
	if cap(r.writes) > keptRedo {
		r.writes = nil
	}
	r.writes = slices.Grow(r.writes[:0], n)
	r.keys = r.keys[:0]
}

// done lets go of what the commit's writes named.
func (r *redoScratch) done() {
	clear(r.writes)
	r.writes = r.writes[:0]
}
