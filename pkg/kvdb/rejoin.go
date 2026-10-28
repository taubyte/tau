package kvdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
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
// no peer that is not rejoining, nor answered a rejoin it could end, for three
// rebroadcast intervals, is rejoining: it records the fleet time as of which
// it last held all its peers held (its gap_from, see settle.go), reports that
// and nothing later, walks nobody's heads, and nobody walks its. Every
// broadcast says so. A peer that can judge answers: ok when it holds all the
// fleet holds and can still refute anything removed since the gap_from (see
// stale), rebase when it cannot refute it. An ok ends the gap once the replica
// holds what the judge vouched with. A rebase replaces everything the replica
// holds with the judge's state and writes again, under a new incarnation, what
// it wrote that the judge does not cover: its own operations are never lost to
// a rebase.
//
// An ok is taken only from a judge that held all the fleet held as of a
// fleet time at least as recent as the gap_from, a rebase only from one that
// has heard a fleet that recent (authoritative), either only from a judge
// whose clock is not behind the replica's, and only for the incarnation it
// names. A replica that has never heard a fleet does not judge, and a
// rejoining one judges only while it hears no live peer: it may be the one
// that is stale.
//
// Every broadcast from a replica that is not rejoining is judged too: one
// that reports a fleet time this replica could no longer vouch for is told to
// rebase, and its heads are not walked.

const (
	rejoinKey = "j" // under the context: the gap_from while rejoining
	rebaseKey = "x" // under the context: a rebase whose writes no snapshot carries yet, empty until they are written
	erasedKey = "e" // under the context: per replaced incarnation, the dots it wrote and no longer held
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

func (store *Datastore) erasedPrefix() ds.Key {
	return store.namespace.ChildString(contextNs).ChildString(erasedKey)
}

func (store *Datastore) erasedKey(replica string) ds.Key {
	return store.erasedPrefix().ChildString(replicaSegment(replica))
}

func encodeErased(k *known) []byte {
	b := binary.AppendUvarint(nil, k.changed)
	b = binary.AppendUvarint(b, k.expires)
	for _, s := range k.spans {
		b = binary.AppendUvarint(b, s.lo)
		b = binary.AppendUvarint(b, s.hi)
	}
	return b
}

func (store *Datastore) loadErased(ctx context.Context) (map[string]*known, error) {
	out := map[string]*known{}
	ep := store.erasedPrefix()
	err := scanStore(ctx, store.store, ep, func(k string, v []byte) error {
		replica, err := segEncoding.DecodeString(strings.TrimPrefix(strings.TrimPrefix(k, ep.String()+"/"), "r"))
		if err != nil {
			return err
		}
		f, rest, err := uvarints(v, 2)
		if err != nil {
			return err
		}
		e := &known{changed: f[0], expires: f[1]}
		for len(rest) > 0 {
			var s []uint64
			if s, rest, err = uvarints(rest, 2); err != nil {
				return err
			}
			e.insert(span{lo: s[0], hi: s[1]})
		}
		out[string(replica)] = e
		return nil
	})
	return out, err
}

func (store *Datastore) ownPrefix() ds.Key { return store.namespace.ChildString(ownNs) }

func (store *Datastore) ownKey(d dot) ds.Key { return store.ownPrefix().ChildString(d.segment()) }

// A message is a decoded broadcast.
type message struct {
	heads       []Head
	since, now  uint64
	horizon     uint64
	rejoining   bool
	incarnation string
	verdict     *verdict
	acks        *causalContext
	context     *causalContext
	settled     bool
	peers       []string
	sent        uint64
	turned      uint64
}

type verdict struct {
	to     string
	rebase bool
	since  uint64
	held   uint64
}

// decodeBroadcast parses a CRDTBroadcast received via pubsub.
func (store *Datastore) decodeBroadcast(data []byte) (message, error) {
	b := pb.CRDTBroadcast{}
	if err := proto.Unmarshal(data, &b); err != nil {
		return message{}, err
	}
	msg := message{since: b.GetSince(), now: b.GetNow(), sent: b.GetSent(), turned: b.GetTurned(), horizon: b.GetHorizon(), rejoining: b.GetRejoining(), incarnation: string(b.GetIncarnation())}
	msg.acks = contextFromPB(&pb.Context{Vector: b.GetAcks()})
	if b.GetContext() != nil {
		msg.context, msg.settled = contextFromPB(b.GetContext()), b.GetSettled()
		for _, p := range b.GetHeard() {
			msg.peers = append(msg.peers, string(p))
		}
	}
	for _, h := range b.GetHeads() {
		c, err := cid.Cast(h.GetCid())
		if err != nil {
			return message{}, err
		}
		msg.heads = append(msg.heads, Head{Cid: c, Dot: dotFromPB(h.GetDot()), At: h.GetAt(), TTL: h.GetTtl()})
	}
	if v := b.GetVerdict(); v != nil {
		msg.verdict = &verdict{to: string(v.GetTo()), rebase: v.GetRebase(), since: v.GetSince(), held: v.GetHeld()}
	}
	return msg, nil
}

// message is what this replica broadcasts now: heads, its fleet time and
// clock, whether it is rejoining, and a verdict if it answers one. The caller
// holds store.mu.
func (store *Datastore) message(heads []Head, v *verdict) *pb.CRDTBroadcast {
	store.sent++
	b := &pb.CRDTBroadcast{
		Sent:        store.sent,
		Since:       store.broadcastSince(),
		Now:         store.clock(),
		Rejoining:   store.rejoining,
		Incarnation: []byte(store.replica),
		Horizon:     store.horizon,
	}
	for _, h := range heads {
		if h.Cid == cid.Undef {
			continue
		}
		b.Heads = append(b.Heads, &pb.Head{Cid: h.Cid.Bytes(), Dot: h.Dot.toPB(), At: h.At, Ttl: h.TTL})
	}
	if v != nil {
		b.Verdict = &pb.Verdict{To: []byte(v.to), Rebase: v.rebase, Since: v.since, Held: v.held}
		b.Context, b.Heard = store.cc.toPB(), store.heardPeers()
	}
	// What this replica merged lately, of whom: the authors stop keeping it
	// for a rebase.
	up := store.times.up()
	for r, at := range store.merged {
		if up-at > gapIntervals*store.opts.RebroadcastInterval || store.cc.inc[r] == nil {
			delete(store.merged, r)
		}
	}
	for _, r := range store.cc.replicas() {
		if _, ok := store.merged[r]; !ok {
			continue
		}
		k := store.cc.inc[r]
		for _, s := range k.spans {
			b.Acks = append(b.Acks, &pb.Entry{Replica: []byte(r), Base: s.lo, Counter: s.hi})
		}
	}
	return b
}

// encodeBroadcast is a broadcast of heads; a heartbeat of a replica that has
// heard a fleet and is not rejoining carries its frontier too.
func (store *Datastore) encodeBroadcast(ctx context.Context, heads []Head, beat bool) ([]byte, error) {
	store.mu.Lock()
	b := store.message(heads, nil)
	if beat && !store.rejoining && store.since != 0 {
		b.Settled = store.settle(ctx)
		b.Context, b.Heard = store.cc.toPB(), store.heardPeers()
		store.markTurn(b)
	}
	store.mu.Unlock()
	return proto.Marshal(b)
}

// broadcastSince is the fleet time a broadcast reports: reportedSince, or 0
// for a replica that has never heard a fleet, which holds only what it wrote
// itself and can judge nobody. The caller holds store.mu.
func (store *Datastore) broadcastSince() uint64 {
	if store.since == 0 {
		return 0
	}
	return store.reportedSince()
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
	return store.startRejoin(ctx, store.held())
}

// held is the fleet time what this replica holds is as recent as: the last
// one it held all its live peers held at, or the earliest of all if it never
// did. The caller holds store.mu or is opening.
func (store *Datastore) held() uint64 {
	return max(store.settledSince, 1)
}

// startRejoin begins a rejoin from gap, recording it before anything heard
// can move it. The caller holds store.mu or is opening.
func (store *Datastore) startRejoin(ctx context.Context, gap uint64) error {
	if err := store.store.Put(ctx, store.rejoinKey(), binary.AppendUvarint(nil, gap)); err != nil {
		return err
	}
	if err := store.store.Sync(ctx, store.rejoinKey()); err != nil {
		return err
	}
	store.rejoining, store.gapFrom = true, gap
	store.logger.Warnf("rejoining: held what the fleet held at %d", gap)
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
	clear(store.oks)
	// The judge was heard: the next gap counts from here.
	store.lastHeard = store.sinceUp
	return nil
}

// checkGap starts a rejoin when no peer of the fleet has been heard for G,
// and ends one when the only peers heard for G whose clocks agree with this
// one's have never heard a fleet: they hold nothing that could be stale and
// can judge nobody, and this replica, still reporting the fleet time it had
// before its gap, is judged by that time if it ever meets the fleet again. A
// peer whose clock disagrees can judge nobody either, whatever it has heard.
func (store *Datastore) checkGap(ctx context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.since == 0 {
		return nil
	}
	if store.rejoining {
		if err := store.endRejoinIfHeld(ctx); err != nil || !store.rejoining {
			return err
		}
		g := gapIntervals * store.opts.RebroadcastInterval
		up := store.times.up()
		if store.anyHeard != 0 && up-store.anyHeard <= g && (store.fleetHeard == 0 || up-store.fleetHeard > g) {
			store.logger.Warnf("rejoining among replicas that never heard a fleet: carrying on from %d", store.gapFrom)
			return store.endRejoin(ctx, 0)
		}
		return nil
	}
	return store.rejoinIfAway(ctx)
}

// rejoinIfAway starts a rejoin when no peer of the fleet has been heard for
// G. The caller holds store.mu, and the replica has heard a fleet and is not
// rejoining.
func (store *Datastore) rejoinIfAway(ctx context.Context) error {
	if store.times.up()-store.lastHeard <= gapIntervals*store.opts.RebroadcastInterval {
		return nil
	}
	return store.startRejoin(ctx, store.held())
}

// plausible reports whether a peer's clock, read a delivery ago, agrees with
// this replica's within the skew. The caller holds store.mu.
func (store *Datastore) plausible(now uint64) bool {
	clock := store.clock()
	window := durationMillis(store.opts.DeliveryBound + store.opts.ClockSkew)
	return now <= clock+store.times.skew && now+window >= clock
}

// handleBroadcast is what a replica does with what it hears.
func (store *Datastore) handleBroadcast(ctx context.Context, msg message) {
	store.mu.Lock()
	if store.closed || msg.incarnation == store.replica {
		store.mu.Unlock()
		return
	}
	// The absence is judged before what was just heard counts as contact: a
	// replica whose timers stood still, a machine suspended for longer than
	// T, may hear a peer before its own heartbeat checks the gap, and that
	// peer must not vouch for what it held while away.
	if !store.rejoining && store.since != 0 {
		if err := store.rejoinIfAway(ctx); err != nil {
			store.logger.Errorf("checking for a gap: %s", err)
		}
	}
	// Only a peer that is not rejoining and has heard a fleet is the fleet:
	// a replica that hears nothing else is as cut off as one that hears
	// nothing.
	up := store.times.up()
	store.hearPeer(msg.incarnation, up)
	if store.plausible(msg.now) {
		store.anyHeard = up
		if msg.since != 0 {
			store.fleetHeard = up
		}
	}
	// Contact with the fleet is a peer that is not rejoining, has heard a
	// fleet, and whose clock agrees with this one: a peer far ahead or far
	// behind tells no fleet time (hearFleet clamps it away), so a replica
	// hearing only such a peer is as cut off as one hearing nobody.
	if !msg.rejoining && msg.since != 0 && store.plausible(msg.now) {
		if store.liveHeard == 0 || up-store.liveHeard > gapIntervals*store.opts.RebroadcastInterval {
			store.contactFrom = up
		}
		store.lastHeard = up
		store.liveHeard = up
		// What the fleet forgot is known from the moment it is heard, not
		// once its blocks are walked: a judge whose walk is still in flight
		// would vouch for what nobody can refute any more.
		if err := store.hearHorizon(ctx, msg.horizon); err != nil {
			store.logger.Errorf("hearing the horizon: %s", err)
		}
	}
	store.heardFrom(msg.incarnation)
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
	// Any peer in contact that is not waiting to be judged tells the
	// fleet's time, one that has never heard a fleet included: a fleet of
	// new replicas starts its time from each other.
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
	// an orphan branch while it syncs, without waiting on a sender that may
	// be gone.
	if store.heads.Len() == 0 {
		if store.opts.manual {
			store.adoptHeads(ctx, msg.heads)
		} else {
			store.wg.Add(1)
			go func() {
				defer store.wg.Done()
				store.adoptHeads(ctx, msg.heads)
			}()
		}
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
		store.hearFrontier(msg)
		return false, nil, nil
	}
	if !msg.rejoining && msg.since == 0 {
		// It has never heard a fleet: it holds only what it wrote, which
		// nothing can have removed.
		return true, nil, nil
	}
	if msg.rejoining && !store.canJudge() {
		return false, nil, nil
	}
	if store.reportedSince() < msg.since {
		// The sender has heard a more recent fleet: nothing to judge, and
		// nothing stale to walk.
		if !msg.rejoining {
			store.hearFrontier(msg)
		}
		return !msg.rejoining, nil, nil
	}
	stale, err := store.stale(ctx, msg.since)
	if err != nil {
		return false, nil, err
	}
	if !msg.rejoining {
		if !stale {
			store.hearFrontier(msg)
			return true, nil, nil
		}
		// What it holds is not walked, so it is not waited for either.
		delete(store.frontiers, msg.incarnation)
	}
	// A replica whose clock disagrees with this one's cannot hold what this
	// one holds, and a rebase would only draw it a new incarnation again and
	// again: its clock hurts only itself.
	if !store.plausible(msg.now) {
		return false, nil, nil
	}
	held, vouch, settled := store.vouching(ctx)
	if !stale && !vouch {
		return false, nil, nil
	}
	if !store.answering(msg.incarnation) {
		return false, nil, nil
	}
	// An answer the sender takes ends its rejoin, or rebases it, on what
	// this replica holds: the two are one fleet, and hearing it rejoin is no
	// gap. Were it one, two replicas left to each other would pass the
	// rejoin back and forth for ever whenever one took G to end.
	if stale || held >= msg.since {
		store.lastHeard = store.times.up()
	}
	hs, _ := store.heads.List()
	answer := store.message(hs, &verdict{to: msg.incarnation, rebase: stale, since: store.reportedSince(), held: held})
	answer.Settled = settled
	store.markTurn(answer)
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
	// A judge that is not rejoining is a live peer whatever it answers:
	// what it says it holds counts like what any live peer says.
	if !msg.rejoining {
		store.hearFrontier(msg)
	}
	if v.to != store.replica || msg.now+store.times.window < store.clock() {
		store.mu.Unlock()
		return nil
	}
	if !v.rebase {
		// The judge must hold what this replica held when it left, and the
		// rejoin ends once this replica holds what the judge vouched with,
		// not when the ok arrives: a judge never heard again leaves it
		// rejoining, for the next judge to answer. A judge's first ok
		// stands until it is held or dropped: what it vouched with does not
		// move, and a judge that writes would otherwise move it again with
		// every answer, before this replica could reach it. It only loses
		// the entries the judge has forgotten since, by expiry, as a later
		// ok shows: nobody may hold them any more, and what the judge no
		// longer knows it no longer refutes either, as its later ok says.
		if !store.rejoining || msg.context == nil || v.held < store.reportedSince() {
			store.mu.Unlock()
			return nil
		}
		if o := store.oks[msg.incarnation]; o == nil {
			store.oks[msg.incarnation] = &ok{since: v.since, held: v.held, cc: msg.context, heard: store.times.up()}
		} else {
			cc := newCausalContext()
			for r, k := range o.cc.inc {
				if msg.context.inc[r] != nil {
					cc.inc[r] = k
				}
			}
			o.cc = cc
		}
		gen := store.generation
		store.mu.Unlock()
		store.walkHeads(ctx, gen, msg.heads)
		store.mu.Lock()
		defer store.mu.Unlock()
		return store.endRejoinIfHeld(ctx)
	}
	if v.since < store.reportedSince() {
		store.mu.Unlock()
		return nil
	}
	return store.rebase(ctx, v, msg)
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
	// still holds when it first hears one is logged then (hearFleet).
	if m.store.since == 0 || len(delta.GetElements())+len(delta.GetTombstones()) == 0 {
		return nil
	}
	rec, err := proto.Marshal(&pb.Delta{Elements: delta.GetElements(), Tombstones: delta.GetTombstones(), At: delta.GetAt(), Ttl: delta.GetTtl(), Priority: delta.GetPriority()})
	if err != nil {
		return err
	}
	m.t.put(m.store.ownKey(d), rec)
	m.added.own = min(m.added.own, delta.GetAt()+delta.GetTtl())
	return nil
}

// settleOwn forgets the own operations whose delta has expired, outside a
// rejoin: no replica will merge that delta any more, and what it did is in
// this replica's state, which its snapshots carry. While rejoining they are
// kept, for a rebase, and so they are while a rebase's writes are carried by
// no snapshot yet: what it put back is in no block, and a second rebase
// before one would drop it. The caller holds store.mu.
func (store *Datastore) settleOwn(ctx context.Context) error {
	now := store.dropClock()
	if store.rejoining || !store.expired(store.lows.own, now) {
		return nil
	}
	if marked, err := store.store.Has(ctx, store.rebaseKey()); err != nil || marked {
		return err
	}
	t := newTxn(store.store)
	low := none
	err := scanStore(ctx, store.store, store.ownPrefix(), func(k string, v []byte) error {
		rec := &pb.Delta{}
		if err := proto.Unmarshal(v, rec); err != nil {
			return err
		}
		if store.expired(rec.GetAt()+rec.GetTtl(), now) {
			t.del(ds.RawKey(k))
		} else {
			low = min(low, rec.GetAt()+rec.GetTtl())
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := t.commit(ctx); err != nil {
		return err
	}
	store.lows.own = low
	return nil
}

// reached forgets the own operations a peer's context covers: that peer has
// merged them, so they are in the fleet whatever happens to this replica.
// The caller holds store.mu.
func (store *Datastore) reached(ctx context.Context, theirs *causalContext) error {
	if theirs == nil || len(theirs.inc) == 0 || store.lows.own == none {
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

// rebase replaces what this replica holds with the judge's state, keeping
// what it wrote that the judge does not cover.
//
// It keeps its own operations not known to have reached the fleet, with the
// value of every element of them still live here; draws a new incarnation,
// so a verdict addressed to the old one is ignored and every walk begun
// before is dropped when it merges; empties the rest of its namespace and
// writes the kept operations again: a put as its element, a removal with its
// target. The marker it leaves until the writes are done makes a crash in
// between write them again at the next open. Then it walks the judge's
// heads.
//
// The caller holds store.mu, which rebase releases. Only the drop and the
// writes are done before it returns, so nothing in between sees a state
// without them: a second rebase, a maintenance pass, a write. The walk goes
// on in the background, so a judge that vanishes leaves nothing waiting on
// its heads but that walk, and no snapshot is written until it ends: the
// kept operations are put back before the judge's state can refute them,
// and a snapshot taken in between would carry what it refutes to replicas
// that may have forgotten the refutation.
func (store *Datastore) rebase(ctx context.Context, v *verdict, msg message) error {
	if store.closed {
		store.mu.Unlock()
		return ErrClosed
	}
	store.logger.Warnf("rebasing on a judge that heard the fleet at %d", v.since)
	if err := store.dropAllButOwn(ctx, v.since); err != nil {
		store.mu.Unlock()
		return err
	}
	gen := store.generation
	store.rebuilding = gen
	store.mu.Unlock()
	if store.opts.manual {
		return store.rebuild(ctx, gen, v.held, msg)
	}
	store.wg.Add(1)
	go func() {
		defer store.wg.Done()
		if err := store.rebuild(ctx, gen, v.held, msg); err != nil && ctx.Err() == nil {
			store.logger.Errorf("rebase: %s", err)
		}
	}()
	return nil
}

// rebuild walks the judge's heads, takes the time the judge held all the
// fleet held at once it holds what the judge did, and compacts.
func (store *Datastore) rebuild(ctx context.Context, gen, held uint64, msg message) error {
	if err := store.handleBranchAt(ctx, gen, linksOf(msg.heads)); err != nil {
		store.logger.Warnf("rebase: the judge's state is not all here yet: %s", err)
		store.MarkDirty(ctx)
	}
	store.mu.Lock()
	if store.rebuilding == gen {
		store.rebuilding = 0
	}
	if gen == store.generation && held != 0 && msg.context != nil && store.holds(msg.context) {
		if err := store.setSettled(ctx, held); err != nil {
			store.mu.Unlock()
			return err
		}
		store.grounded = true
	}
	store.mu.Unlock()
	// What was put back reaches the fleet in a snapshot, now rather than at
	// the next maintenance.
	_, err := store.compact(ctx, true)
	return err
}

func linksOf(heads []Head) []link {
	out := make([]link, 0, len(heads))
	for _, h := range heads {
		out = append(out, h.link())
	}
	return out
}

// dropAllButOwn empties the namespace but for the format, the shutdown mark
// and the own operations, which keep the values still live, starts over with
// the judge's fleet time under a new incarnation, and writes the own
// operations again. The caller holds store.mu.
//
// What the old incarnation wrote and no longer holds was removed, by this
// replica or by a removal it merged, and a removal it made before it first
// heard a fleet is in no log. Those dots are kept as erased, removals written
// now that live as long as an entry would: a copy of one, in the judge's
// state or anyone's, is refused here and becomes a pending removal, which
// the next snapshot takes wherever it goes. Left out of them are the
// elements still held, so what was acknowledged and is not written again
// stays live, and the logged operations, which reissue writes again.
//
// They are not put in the new context. A context covers a dot only once it
// merged it, or a snapshot that did: the old incarnation's snapshots and the
// deltas that removed only are among those dots, and a context claiming them
// would skip the judge's state they hold, and tell every peer that merged
// them that a snapshot of this replica holds it.
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
	held := map[uint64]struct{}{}
	for k, rec := range own {
		d, err := parseDotSegment(strings.TrimPrefix(k, prefix))
		if err != nil {
			return err
		}
		if d.replica == store.replica {
			held[d.counter] = struct{}{}
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
	ep := store.set.elemsPrefix().String() + "/"
	var keys []string
	op := store.ownedPrefix()
	err = scanStore(ctx, store.store, store.namespace, func(k string, v []byte) error {
		switch {
		case strings.HasPrefix(k, prefix),
			strings.HasPrefix(k, store.erasedPrefix().String()+"/"),
			strings.HasPrefix(k, store.reclaimPrefix().String()+"/"),
			k == store.versionKey().String(),
			k == store.badShutdownKey().String(),
			k == store.namespace.ChildString(redoKey).String(),
			k == store.appliedKey().String():
			return nil
		case strings.HasPrefix(k, op.String()+"/"):
			// Nothing owns a state block any more: reclaim removes them.
			c, err := cidOfBlockKey(op, k)
			if err != nil {
				return err
			}
			t.put(store.reclaimKey(c), nil)
		case strings.HasPrefix(k, bp.String()+"/"):
			c, err := cidOfBlockKey(bp, k)
			if err != nil {
				return err
			}
			blocks = append(blocks, c)
		case strings.HasPrefix(k, ep):
			el, err := store.set.parseElem(k, v)
			if err != nil {
				return err
			}
			if el.dot.replica == store.replica {
				held[el.dot.counter] = struct{}{}
			}
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
	var erased *known
	if k := store.cc.inc[store.replica]; k != nil {
		at := max(store.clock(), since)
		erased = &known{spans: removedSpans(k.spans, held), changed: at, expires: max(at+store.retention(), k.expires)}
		if len(erased.spans) > 0 {
			t.put(store.erasedKey(store.replica), encodeErased(erased))
		}
	}
	t.put(store.sinceKey(), binary.AppendUvarint(nil, since))
	t.del(store.settledKey())
	t.put(store.rebaseKey(), nil)
	store.gcMu.Lock()
	err = store.commitAtomic(ctx, t)
	store.gcMu.Unlock()
	if err != nil {
		return err
	}
	store.base = stateBase{}
	if len(blocks) > 0 {
		if err := store.dagService.RemoveMany(ctx, blocks); err != nil {
			store.logger.Warnf("rebase: removing the old blocks: %s", err)
		}
	}

	if erased != nil && len(erased.spans) > 0 {
		store.erased[store.replica] = erased
	}
	store.generation++
	if err := store.drawIncarnation(); err != nil {
		return err
	}
	store.heads.reset()
	store.lows.pending, store.lows.block = none, none
	store.setContext(newCausalContext())
	store.liveCount, store.blockCount = 0, 0
	store.horizon = 0
	store.since, store.sinceUp = since, store.times.up()
	store.rejoining, store.gapFrom = false, 0
	store.settledSince = 0
	clear(store.oks)
	store.lastHeard = store.sinceUp
	store.seenHeadsMux.Lock()
	clear(store.seenHeads)
	store.seenHeadsMux.Unlock()
	for _, k := range gone {
		store.set.deleteHook(k)
	}
	return store.reissueLocked(ctx)
}

// removedSpans is spans less the counters held: the dots of an incarnation
// that name no element held any more.
func removedSpans(spans []span, held map[uint64]struct{}) []span {
	counters := make([]uint64, 0, len(held))
	for c := range held {
		counters = append(counters, c)
	}
	slices.Sort(counters)
	var out []span
	for _, s := range spans {
		lo := s.lo
		i, _ := slices.BinarySearch(counters, s.lo+1)
		for ; i < len(counters) && counters[i] <= s.hi; i++ {
			if counters[i]-1 > lo {
				out = append(out, span{lo: lo, hi: counters[i] - 1})
			}
			lo = counters[i]
		}
		if s.hi > lo {
			out = append(out, span{lo: lo, hi: s.hi})
		}
	}
	return out
}

// reissue puts back every kept own operation the state does not cover,
// forgets those it covers, and marks the rebase written: the next snapshot,
// which carries what it put back, forgets the mark. The others stay in the
// log until a peer acknowledges them or they expire, as before the rebase:
// nothing says the snapshot that carries them reached anyone, and a second
// rebase must put them back again.
//
// An element goes back under its own dot, as an ancient one, not as a new
// write: if the fleet removed it while this replica was away and still knows
// it, by the writer's entry or a pending removal, it is refused here, or
// removed here when the judge's state is merged, and refused wherever this
// replica's snapshot takes it, exactly as the stale copy would have been. A
// new dot would escape a removal of the old one. A removal goes out again as
// a new delta naming its target. The next snapshot, once the judge's state
// is walked, takes the elements to the fleet.
func (store *Datastore) reissue(ctx context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.reissueLocked(ctx)
}

// carried forgets a rebase once a snapshot carries what it put back: the one
// just written. A rebase whose writes are not done stays marked for the next
// open, and so does one no snapshot has carried yet. The caller holds
// store.mu.
func (store *Datastore) carried(ctx context.Context) error {
	mark, err := store.store.Get(ctx, store.rebaseKey())
	if errors.Is(err, ds.ErrNotFound) || err == nil && len(mark) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	return store.store.Delete(ctx, store.rebaseKey())
}

// reissueLocked is reissue. The caller holds store.mu.
func (store *Datastore) reissueLocked(ctx context.Context) error {
	mark, err := store.store.Get(ctx, store.rebaseKey())
	if errors.Is(err, ds.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(mark) > 0 {
		// Written, and not carried yet: a crash came in between.
		store.compactDue = true
		return nil
	}
	delta := store.stamped(&pb.Delta{})
	m := store.beginMerge()
	var done []ds.Key
	kept := map[ds.Key]*pb.Delta{}
	prefix := store.ownPrefix().String() + "/"
	err = scanStore(ctx, store.store, store.ownPrefix(), func(k string, v []byte) error {
		d, err := parseDotSegment(strings.TrimPrefix(k, prefix))
		if err != nil {
			return err
		}
		if store.cc.covers(d) {
			done = append(done, ds.RawKey(k))
			return nil
		}
		rec := &pb.Delta{}
		if err := proto.Unmarshal(v, rec); err != nil {
			return err
		}
		// The removals go out in a delta of their own, logged as it merges;
		// the record keeps the elements.
		if len(rec.GetElements()) == 0 {
			done = append(done, ds.RawKey(k))
		} else if len(rec.GetTombstones()) > 0 {
			kept[ds.RawKey(k)] = &pb.Delta{Elements: rec.GetElements(), At: rec.GetAt(), Ttl: rec.GetTtl(), Priority: rec.GetPriority()}
		}
		for _, e := range rec.GetElements() {
			el := element{key: e.GetKey(), dot: d, priority: rec.GetPriority(), value: e.GetValue(), at: rec.GetAt(), ttl: rec.GetTtl()}
			if err := m.admit(ctx, el, store.cc); err != nil {
				return err
			}
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
	// The elements, then the removals, then the record of both: a crash in
	// between does it again, and putting back an element that is here, or
	// removing one that is gone, changes nothing.
	if err := m.commit(ctx); err != nil {
		return err
	}
	if len(delta.Tombstones) > 0 {
		if _, _, err := store.publishLocked(ctx, delta); err != nil {
			return fmt.Errorf("reissuing own removals: %w", err)
		}
	}
	t := newTxn(store.store)
	for _, k := range done {
		t.del(k)
	}
	for k, rec := range kept {
		b, err := proto.Marshal(rec)
		if err != nil {
			return err
		}
		t.put(k, b)
	}
	t.put(store.rebaseKey(), []byte{1})
	if err := t.commit(ctx); err != nil {
		return err
	}
	store.compactDue = true
	return nil
}
