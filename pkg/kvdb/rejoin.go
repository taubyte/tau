package kvdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"google.golang.org/protobuf/proto"
)

// A replica that has been out of contact may carry elements the fleet has
// removed and has since forgotten removing. It must not merge into the fleet
// on its own word (T2 A1), and its own clock cannot say how long it was away.
//
// So a replica that opens with anything heard from a fleet, or that has heard
// no peer for three rebroadcast intervals, is rejoining: it records the fleet
// time it had heard before (its gap_from), reports that and nothing later,
// walks nobody's heads, and nobody walks its. Every broadcast says so. A peer
// that can judge answers: ok when it can still refute anything removed since
// the gap_from (see stale), rebase when it cannot. Ok ends the gap. Rebase
// replaces everything the replica holds with the judge's state and writes
// again, under a new incarnation, what it wrote that the judge does not
// cover: its own operations are never lost to a rebase.
//
// A verdict is taken only from a judge that has heard at least as recent a
// fleet as the replica had (authoritative), whose clock is not behind the
// replica's, and only for the incarnation it names. A replica that has never
// heard a fleet does not judge, and a rejoining one judges only while it hears
// no live peer: it may be the one that is stale.
//
// Every broadcast from a replica that is not rejoining is judged too: one
// that reports a fleet time this replica could no longer vouch for is told to
// rebase, and its heads are not walked.

const (
	rejoinKey = "j" // under the context: the gap_from while rejoining
	rebaseKey = "x" // under the context: a rebase whose writes are not reissued yet
	ownNs     = "o" // own operations not known to have reached the fleet
)

// gapIntervals is G, in rebroadcast intervals: hearing no peer for longer
// starts a rejoin.
const gapIntervals = 3

func (store *Datastore) rejoinKey() ds.Key {
	return store.namespace.ChildString(contextNs).ChildString(rejoinKey)
}

func (store *Datastore) rebaseKey() ds.Key {
	return store.namespace.ChildString(contextNs).ChildString(rebaseKey)
}

func (store *Datastore) ownPrefix() ds.Key { return store.namespace.ChildString(ownNs) }

func (store *Datastore) ownKey(d dot) ds.Key { return store.ownPrefix().ChildString(d.segment()) }

// A message is a decoded broadcast.
type message struct {
	heads       []Head
	since, now  uint64
	rejoining   bool
	incarnation string
	verdict     *verdict
	acks        *causalContext
}

type verdict struct {
	to     string
	rebase bool
	since  uint64
}

// decodeBroadcast parses a CRDTBroadcast received via pubsub.
func (store *Datastore) decodeBroadcast(data []byte) (message, error) {
	b := pb.CRDTBroadcast{}
	if err := proto.Unmarshal(data, &b); err != nil {
		return message{}, err
	}
	msg := message{since: b.GetSince(), now: b.GetNow(), rejoining: b.GetRejoining(), incarnation: string(b.GetIncarnation())}
	msg.acks = contextFromPB(&pb.Context{Vector: b.GetAcks()})
	for _, h := range b.GetHeads() {
		c, err := cid.Cast(h.GetCid())
		if err != nil {
			return message{}, err
		}
		msg.heads = append(msg.heads, Head{Cid: c, Dot: dotFromPB(h.GetDot()), At: h.GetAt(), TTL: h.GetTtl()})
	}
	if v := b.GetVerdict(); v != nil {
		msg.verdict = &verdict{to: string(v.GetTo()), rebase: v.GetRebase(), since: v.GetSince()}
	}
	return msg, nil
}

// message is what this replica broadcasts now: heads, its fleet time and
// clock, whether it is rejoining, and a verdict if it answers one. The caller
// holds store.mu.
func (store *Datastore) message(heads []Head, v *verdict) *pb.CRDTBroadcast {
	b := &pb.CRDTBroadcast{
		Since:       store.reportedSince(),
		Now:         store.clock(),
		Rejoining:   store.rejoining,
		Incarnation: []byte(store.replica),
	}
	for _, h := range heads {
		if h.Cid == cid.Undef {
			continue
		}
		b.Heads = append(b.Heads, &pb.Head{Cid: h.Cid.Bytes(), Dot: h.Dot.toPB(), At: h.At, Ttl: h.TTL})
	}
	if v != nil {
		b.Verdict = &pb.Verdict{To: []byte(v.to), Rebase: v.rebase, Since: v.since}
	}
	// What incarnations wrote lately and this replica has merged: their
	// authors stop keeping it for a rebase.
	recent := store.fleetNow()
	if span := gapIntervals * durationMillis(store.opts.RebroadcastInterval); recent > span {
		recent -= span
	} else {
		recent = 0
	}
	for _, r := range store.cc.replicas() {
		if k := store.cc.inc[r]; k.changed >= recent {
			b.Acks = append(b.Acks, &pb.Entry{Replica: []byte(r), Base: k.spans[0].lo, Counter: k.spans[0].hi})
		}
	}
	return b
}

func (store *Datastore) encodeBroadcast(heads []Head) ([]byte, error) {
	store.mu.Lock()
	b := store.message(heads, nil)
	store.mu.Unlock()
	return proto.Marshal(b)
}

// reportedSince is the fleet time this replica vouches for: its gap_from
// while rejoining, what it holds otherwise. The caller holds store.mu.
func (store *Datastore) reportedSince() uint64 {
	if store.rejoining {
		return store.gapFrom
	}
	return store.knownSince()
}

// loadRejoin restores the rejoin state at open: a rejoin interrupted by a
// crash goes on with the gap_from it had, and a replica that has heard a
// fleet before starts one, since it cannot tell how long it was closed.
func (store *Datastore) loadRejoin(ctx context.Context) error {
	gap, err := store.store.Get(ctx, store.rejoinKey())
	switch {
	case err == nil:
		v, n := binary.Uvarint(gap)
		if n <= 0 {
			return errors.New("error decoding the rejoin record")
		}
		store.rejoining, store.gapFrom = true, v
		return nil
	case !errors.Is(err, ds.ErrNotFound):
		return err
	}
	if store.since == 0 {
		return nil
	}
	return store.startRejoin(ctx)
}

// startRejoin begins a rejoin from the fleet time last heard, recording it
// before anything heard can move it. The caller holds store.mu or is
// opening.
func (store *Datastore) startRejoin(ctx context.Context) error {
	gap := store.since
	if err := store.store.Put(ctx, store.rejoinKey(), binary.AppendUvarint(nil, gap)); err != nil {
		return err
	}
	if err := store.store.Sync(ctx, store.rejoinKey()); err != nil {
		return err
	}
	store.rejoining, store.gapFrom = true, gap
	store.logger.Warnf("rejoining: last heard the fleet at %d", gap)
	return nil
}

// endRejoin ends a rejoin on an ok: the fleet time heard is the judge's. The
// caller holds store.mu.
func (store *Datastore) endRejoin(ctx context.Context, since uint64) error {
	t := newTxn(store.store)
	t.del(store.rejoinKey())
	since = max(since, store.gapFrom, store.since)
	t.put(store.sinceKey(), binary.AppendUvarint(nil, since))
	if err := t.commit(ctx); err != nil {
		return err
	}
	store.rejoining, store.gapFrom = false, 0
	store.since, store.sinceUp = since, store.times.up()
	return nil
}

// checkGap starts a rejoin when no peer has been heard for G.
func (store *Datastore) checkGap(ctx context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.rejoining || store.since == 0 {
		return nil
	}
	if store.times.up()-store.lastHeard <= gapIntervals*store.opts.RebroadcastInterval {
		return nil
	}
	return store.startRejoin(ctx)
}

// handleBroadcast is what a replica does with what it hears.
func (store *Datastore) handleBroadcast(ctx context.Context, msg message) {
	store.mu.Lock()
	if store.closed || msg.incarnation == store.replica {
		store.mu.Unlock()
		return
	}
	store.lastHeard = store.times.up()
	if !msg.rejoining {
		store.liveHeard = store.lastHeard
	}
	gen := store.generation
	err := store.reached(ctx, msg.acks)
	store.mu.Unlock()
	if err != nil {
		store.logger.Errorf("taking acks: %s", err)
	}

	store.seenHeadsMux.Lock()
	for _, head := range msg.heads {
		store.seenHeads[head.Cid] = struct{}{}
	}
	store.seenHeadsMux.Unlock()

	if v := msg.verdict; v != nil && v.to == store.currentIncarnation() {
		if err := store.onVerdict(ctx, msg); err != nil {
			store.logger.Errorf("taking a verdict: %s", err)
		}
		return
	}
	if !msg.rejoining {
		if err := store.hearFleet(ctx, msg.now); err != nil {
			store.logger.Errorf("hearing the fleet: %s", err)
		}
	}
	walk, answer, err := store.judge(ctx, msg)
	if err != nil {
		store.logger.Errorf("judging %x: %s", msg.incarnation, err)
		return
	}
	if answer != nil && store.broadcaster != nil {
		if err := store.broadcaster.Broadcast(ctx, answer); err != nil && ctx.Err() == nil {
			store.logger.Warnf("answering a rejoin: %s", err)
		}
	}
	if !walk {
		return
	}

	// A replica with no heads builds on what it heard rather than starting
	// an orphan branch while it syncs.
	if store.heads.Len() == 0 {
		store.adoptHeads(ctx, msg.heads)
	}
	store.walkHeads(ctx, gen, msg.heads)
}

func (store *Datastore) currentIncarnation() string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.replica
}

// walkHeads walks every head on its own: a walk stuck fetching a block its
// author has since compacted away must not hold up the snapshot, heard
// later, that covers it and so ends the stuck walk.
func (store *Datastore) walkHeads(ctx context.Context, gen uint64, heads []Head) {
	if store.opts.manual {
		// A test driving time walks in step with what it delivers.
		for _, h := range heads {
			if err := store.handleBlockAt(ctx, gen, h); err != nil {
				store.logger.Errorf("error processing new head: %s", err)
			}
		}
		return
	}
	for _, head := range heads {
		store.wg.Add(1)
		go func(h Head) {
			defer store.wg.Done()
			if err := store.handleBlockAt(ctx, gen, h); err != nil {
				store.logger.Errorf("error processing new head: %s", err)
			}
		}(head)
	}
}

// judge answers a rejoining sender if it can, tells a sender that is not
// rejoining to rebase if it could no longer vouch for it, and reports whether
// the sender's heads are to be walked, and the answer to broadcast if any.
func (store *Datastore) judge(ctx context.Context, msg message) (bool, []byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !msg.rejoining && store.rejoining {
		return false, nil, nil
	}
	if msg.rejoining && !store.canJudge() {
		return false, nil, nil
	}
	if store.reportedSince() < msg.since {
		// The sender has heard a more recent fleet: nothing to judge, and
		// nothing stale to walk.
		return !msg.rejoining, nil, nil
	}
	stale, err := store.stale(ctx, msg.since)
	if err != nil {
		return false, nil, err
	}
	if !msg.rejoining && !stale {
		return true, nil, nil
	}
	if !store.answering(msg.incarnation) {
		return false, nil, nil
	}
	hs, _ := store.heads.List()
	answer := store.message(hs, &verdict{to: msg.incarnation, rebase: stale, since: store.reportedSince()})
	if stale {
		store.logger.Warnf("%x last heard the fleet at %d: telling it to rebase", msg.incarnation, msg.since)
	}
	data, err := proto.Marshal(answer)
	return false, data, err
}

// canJudge reports whether this replica may answer a rejoining one: it has
// heard a fleet, and it is not rejoining itself while a live peer, who would
// judge better, is in earshot. The caller holds store.mu.
func (store *Datastore) canJudge() bool {
	if store.since == 0 {
		return false
	}
	if !store.rejoining {
		return true
	}
	return store.liveHeard == 0 || store.times.up()-store.liveHeard > gapIntervals*store.opts.RebroadcastInterval
}

// answering reports whether to answer an incarnation now: at most once per
// rebroadcast interval each. The caller holds store.mu.
func (store *Datastore) answering(inc string) bool {
	now := store.times.up()
	if last, ok := store.answered[inc]; ok && now-last < store.opts.RebroadcastInterval {
		return false
	}
	for k, v := range store.answered {
		if now-v >= store.opts.RebroadcastInterval {
			delete(store.answered, k)
		}
	}
	store.answered[inc] = now
	return true
}

// onVerdict takes a verdict addressed to this incarnation from a judge it
// can trust with it.
func (store *Datastore) onVerdict(ctx context.Context, msg message) error {
	v := msg.verdict
	store.mu.Lock()
	if v.to != store.replica || v.since < store.reportedSince() || msg.now+store.times.window < store.clock() {
		store.mu.Unlock()
		return nil
	}
	if !v.rebase {
		if !store.rejoining {
			store.mu.Unlock()
			return nil
		}
		err := store.endRejoin(ctx, v.since)
		gen := store.generation
		store.mu.Unlock()
		if err != nil {
			return err
		}
		store.logger.Infof("rejoined: %x vouched for what this replica holds", msg.incarnation)
		store.walkHeads(ctx, gen, msg.heads)
		return nil
	}
	store.mu.Unlock()
	return store.rebase(ctx, v.since, msg.heads)
}

// heartbeat is one rebroadcast: start a rejoin if no peer was heard for G,
// then announce the heads nobody else announced, with this replica's fleet
// time, even when there are none.
func (store *Datastore) heartbeat(ctx context.Context) {
	if err := store.checkGap(ctx); err != nil {
		store.logger.Errorf("checking for a gap: %s", err)
	}
	store.rebroadcastHeads(ctx)
}

// logOwn records, in a merge of this replica's own delta, what the delta
// wrote, for as long as it may not have reached the fleet: a rebase writes it
// again. The caller holds store.mu.
func (m *merging) logOwn(d dot, delta *pb.Delta) error {
	// A replica that has never heard a fleet is never judged stale: what it
	// wrote reaches whoever it meets from its own heads.
	if m.store.since == 0 || len(delta.GetElements())+len(delta.GetTombstones()) == 0 {
		return nil
	}
	rec, err := proto.Marshal(&pb.Delta{Elements: delta.GetElements(), Tombstones: delta.GetTombstones(), At: delta.GetAt()})
	if err != nil {
		return err
	}
	m.t.put(m.store.ownKey(d), rec)
	return nil
}

// reached forgets the own operations a peer's context covers: that peer has
// merged them, so they are in the fleet whatever happens to this replica.
// The caller holds store.mu.
func (store *Datastore) reached(ctx context.Context, theirs *causalContext) error {
	if theirs == nil || len(theirs.inc) == 0 {
		return nil
	}
	t := newTxn(store.store)
	prefix := store.ownPrefix().String() + "/"
	err := scanStore(ctx, store.store, store.ownPrefix(), func(k string, _ []byte) error {
		d, err := parseDotSegment(strings.TrimPrefix(k, prefix))
		if err != nil {
			return err
		}
		if theirs.covers(d) {
			t.del(ds.RawKey(k))
		}
		return nil
	})
	if err != nil {
		return err
	}
	return t.commit(ctx)
}

// rebase replaces what this replica holds with the judge's state and writes
// again what it wrote that the judge does not cover.
//
// It keeps its own operations not known to have reached the fleet, with the
// value of every element of them still live here; draws a new incarnation,
// so a verdict addressed to the old one is ignored and every walk begun
// before is dropped when it merges; empties the rest of its namespace; walks
// the judge's heads; and writes the kept operations again: a put as a new
// element, a removal with its target. The marker it leaves until the writes
// are done makes a crash in between write them again at the next open.
func (store *Datastore) rebase(ctx context.Context, since uint64, heads []Head) error {
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return ErrClosed
	}
	store.logger.Warnf("rebasing on a judge that heard the fleet at %d", since)
	if err := store.dropAllButOwn(ctx, since); err != nil {
		store.mu.Unlock()
		return err
	}
	gen := store.generation
	store.mu.Unlock()

	if err := store.handleBranchAt(ctx, gen, linksOf(heads)); err != nil {
		store.logger.Warnf("rebase: the judge's state is not all here yet: %s", err)
		store.MarkDirty(ctx)
	}
	return store.reissue(ctx)
}

func linksOf(heads []Head) []link {
	out := make([]link, 0, len(heads))
	for _, h := range heads {
		out = append(out, h.link())
	}
	return out
}

// dropAllButOwn empties the namespace but for the format, the shutdown mark
// and the own operations, which keep the values still live, and starts over
// with the judge's fleet time under a new incarnation. The caller holds
// store.mu.
func (store *Datastore) dropAllButOwn(ctx context.Context, since uint64) error {
	own := map[string]*pb.Delta{}
	err := scanStore(ctx, store.store, store.ownPrefix(), func(k string, v []byte) error {
		rec := &pb.Delta{}
		if err := proto.Unmarshal(v, rec); err != nil {
			return err
		}
		own[k] = rec
		return nil
	})
	if err != nil {
		return err
	}
	t := newTxn(store.store)
	prefix := store.ownPrefix().String() + "/"
	for k, rec := range own {
		d, err := parseDotSegment(strings.TrimPrefix(k, prefix))
		if err != nil {
			return err
		}
		var live []*pb.Element
		for _, e := range rec.GetElements() {
			here, err := store.store.Has(ctx, store.set.elemKey(e.GetKey(), d))
			if err != nil {
				return err
			}
			if here {
				live = append(live, e)
			}
		}
		rec.Elements = live
		b, err := proto.Marshal(rec)
		if err != nil {
			return err
		}
		t.put(ds.RawKey(k), b)
	}

	var blocks []cid.Cid
	bp := store.blocksPrefix()
	var keys []string
	err = scanStore(ctx, store.store, store.namespace, func(k string, _ []byte) error {
		switch {
		case strings.HasPrefix(k, prefix),
			k == store.versionKey().String(),
			k == store.badShutdownKey().String():
			return nil
		case strings.HasPrefix(k, bp.String()+"/"):
			c, err := cidOfBlockKey(bp, k)
			if err != nil {
				return err
			}
			blocks = append(blocks, c)
		}
		keys = append(keys, k)
		return nil
	})
	if err != nil {
		return err
	}
	var gone []string
	vp := store.set.namespace.ChildString(keysNs).String()
	for _, k := range keys {
		if strings.HasPrefix(k, vp+"/") && strings.HasSuffix(k, "/"+valueSuffix) {
			gone = append(gone, strings.TrimSuffix(strings.TrimPrefix(k, vp), "/"+valueSuffix))
		}
		t.del(ds.RawKey(k))
	}
	t.put(store.sinceKey(), binary.AppendUvarint(nil, since))
	t.put(store.rebaseKey(), nil)
	if err := t.commit(ctx); err != nil {
		return err
	}
	if len(blocks) > 0 {
		if err := store.dagService.RemoveMany(ctx, blocks); err != nil {
			store.logger.Warnf("rebase: removing the old blocks: %s", err)
		}
	}

	store.generation++
	if err := store.drawIncarnation(); err != nil {
		return err
	}
	store.heads.reset()
	store.setContext(newCausalContext())
	store.liveCount, store.blockCount = 0, 0
	store.horizon = 0
	store.since, store.sinceUp = since, store.times.up()
	store.rejoining, store.gapFrom = false, 0
	store.seenHeadsMux.Lock()
	clear(store.seenHeads)
	store.seenHeadsMux.Unlock()
	for _, k := range gone {
		store.set.deleteHook(k)
	}
	return nil
}

// reissue writes again every kept own operation the state does not cover,
// then forgets them all and the rebase with them.
func (store *Datastore) reissue(ctx context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	marked, err := store.store.Has(ctx, store.rebaseKey())
	if err != nil || !marked {
		return err
	}
	delta := store.stamped(&pb.Delta{})
	var done []ds.Key
	prefix := store.ownPrefix().String() + "/"
	err = scanStore(ctx, store.store, store.ownPrefix(), func(k string, v []byte) error {
		d, err := parseDotSegment(strings.TrimPrefix(k, prefix))
		if err != nil {
			return err
		}
		done = append(done, ds.RawKey(k))
		if store.cc.covers(d) {
			return nil
		}
		rec := &pb.Delta{}
		if err := proto.Unmarshal(v, rec); err != nil {
			return err
		}
		for _, e := range rec.GetElements() {
			delta.Elements = append(delta.Elements, &pb.Element{Key: e.GetKey(), Value: e.GetValue()})
		}
		// A removal written again is needed from now on, at least as long
		// as it was when first written.
		for _, tomb := range rec.GetTombstones() {
			ttl := delta.Ttl
			if until := tomb.GetAt() + tomb.GetTtl(); until > delta.At+ttl {
				ttl = until - delta.At
			}
			delta.Tombstones = append(delta.Tombstones, &pb.Element{Key: tomb.GetKey(), Dot: tomb.GetDot(), At: delta.At, Ttl: ttl})
		}
		return nil
	})
	if err != nil {
		return err
	}
	t := newTxn(store.store)
	for _, k := range done {
		t.del(k)
	}
	t.del(store.rebaseKey())
	if len(delta.Elements)+len(delta.Tombstones) > 0 {
		if _, _, err := store.publishLocked(ctx, delta); err != nil {
			return fmt.Errorf("reissuing own operations: %w", err)
		}
	}
	return t.commit(ctx)
}
