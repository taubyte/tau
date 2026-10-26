package kvdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"slices"
	"time"

	ds "github.com/ipfs/go-datastore"
)

// A judge vouches only for what it holds.
//
// A judge that has heard the fleet's time and horizon but not merged what the
// fleet holds cannot see a removal the fleet holds that has expired and is
// not forgotten yet: it would vouch for a replica only that removal still
// refutes, and once the holders forget it the stale copy comes back. So a
// judge that hears live peers answers ok only when it is settled: it holds
// all that every one of them holds, by the contexts they send in their
// heartbeats (their frontiers), and it knows it has heard them all. A rebase
// is answered either way: it never vouches for anything.
//
// Only live peers count. One not heard for G is gone, and what only it held
// is lost to the fleet the way a removal held by a replica cut off is
// (T5.4); waiting for it would stall every rejoin until it expired. A live
// peer that cannot serve what it holds still counts: its frontier is heard,
// and what it holds is held here only once it is fetched, from it or from
// anyone.
//
// A peer that is not settled may also have heard peers this replica has not,
// across a partition it bridges or one it crossed within G, where it is not
// new: it sends the peers it heard with its frontier, and while one of them
// is not heard here and was first listed less than G ago, this replica is
// not settled. A listed peer is waited for once per turn: one listed for G
// and never heard is gone as any peer not heard for G is, and one heard is
// waited for again only once it has gone unheard for G. A bridge that was
// settled and is not may have just heard what a listed peer holds and not
// fetched it yet: the peers it lists then are waited for again, G from that
// turn, heard or not, until this replica holds a frontier of theirs sent
// after the turn, as one heard after it may have been sent before. Only an
// incarnation that sent a settled frontier turns, so a restart renews
// nothing; a bridge that says it is settled holds nothing back; and a peer
// is named at most G after its last lister heard it, so a gone peer holds
// this replica back at most 2G and a delivery bound.
//
// Frontiers arrive in any order within the delivery bound. One stamped
// before the frontier held of its incarnation is older and does not replace
// it, nor does a settled one stamped the same millisecond as one that is
// not: an older frontier would hide what its sender holds since, or a turn.
//
// A replica knows it has heard every live peer once it has heard the fleet
// for G without a break, or holds the frontier of a peer that was settled,
// or holds the state of a judge that vouched for it or rebased it: it is
// grounded. A rebase keeps it: it empties what the replica holds, not whom
// it has heard, and settled asks for both. A peer first
// heard less than G ago that has not said it is settled may be the first
// heard of peers this replica has not heard yet, a fleet it was cut off from
// among them: until it says so, or G has passed and those have been heard
// too, this replica is not settled. First heard is the peer's, whatever it
// says and under whatever incarnation: one heard again within G is the peer
// heard all along, so a peer that restarts, is rebased or crash-loops holds
// a judge back once, however often it does; one silent for G, as across a
// partition, is new again.
//
// What a replica holds is as recent as the fleet time it was last settled at
// (settledSince), not the fleet time it last heard: a rejoin reports that,
// and a judge that hears no live peer, rejoining or alone, vouches as of it.
//
// No judge is special. Any settled peer answers; a rejoining replica ends its
// rejoin on the ok of whichever judge it first holds the state of, and drops
// the ok of a judge not heard for G; a peer not heard for G counts nowhere.

const settledKey = "v" // under the context: the fleet time this replica last held all its live peers held

func (store *Datastore) settledKey() ds.Key {
	return store.namespace.ChildString(contextNs).ChildString(settledKey)
}

// A frontier is what a live incarnation last said it holds, its clock when
// it said so, when it was last heard, and when its peer was first heard.
type frontier struct {
	cc           *causalContext
	settled      bool
	peers        []string
	at           uint64
	first, heard time.Duration
}

// A contact is a peer heard without a break of G: when this replica began
// hearing it, and when it last did.
type contact struct {
	first, heard time.Duration
}

// A listing is a peer a frontier names that this replica has not heard, or
// any a frontier names on turning unsettled: when one first named it, or
// named it on turning, and when one last did; for a turn, the clock past
// which a frontier of that peer was sent after the turn (the turning
// frontier's own and the skew).
type listing struct {
	first, last time.Duration
	after       uint64
}

// An ok is a verdict that vouched for this replica: its rejoin ends once it
// holds the context the judge vouched with.
type ok struct {
	since, held uint64
	cc          *causalContext
	heard       time.Duration
}

// A forgotten entry is one this replica dropped by expiry, kept while a peer
// that touched it later may still send it.
type forgottenEntry struct {
	k  *known
	at time.Duration
}

// contains reports whether every span of o lies in one of k's.
func (k *known) contains(o *known) bool {
	if k == nil {
		return false
	}
	for _, s := range o.spans {
		if !k.containsSpan(s) {
			return false
		}
	}
	return true
}

func (k *known) containsSpan(s span) bool {
	for _, m := range k.spans {
		if m.lo <= s.lo && s.hi <= m.hi {
			return true
		}
	}
	return false
}

// hearPeer records that the peer an incarnation was drawn by was heard at
// up, forgetting first the peers not heard for G, rejoining or not, and
// returns when this replica began hearing it without a break. The caller
// holds store.mu.
func (store *Datastore) hearPeer(incarnation string, up time.Duration) time.Duration {
	store.prune(up)
	p := peerOf(incarnation)
	c := store.contacts[p]
	if c == nil {
		c = &contact{first: up}
		store.contacts[p] = c
	}
	c.heard = up
	return c.first
}

// hearFrontier records what a live peer says it holds. The caller holds
// store.mu.
func (store *Datastore) hearFrontier(msg message) {
	up := store.times.up()
	first := store.hearPeer(msg.incarnation, up)
	if msg.context == nil || msg.since == 0 || !store.plausible(msg.now) {
		return
	}
	prev := store.frontiers[msg.incarnation]
	if prev != nil && (msg.now < prev.at || msg.now == prev.at && msg.settled && !prev.settled) {
		prev.heard = up
		return
	}
	turned := !msg.settled && prev != nil && prev.settled
	store.frontiers[msg.incarnation] = &frontier{cc: msg.context, settled: msg.settled, peers: msg.peers, at: msg.now, first: first, heard: up}
	if l := store.listed[peerOf(msg.incarnation)]; l != nil && l.after != 0 && msg.now > l.after {
		delete(store.listed, peerOf(msg.incarnation))
	}
	self := peerOf(store.replica)
	for _, p := range msg.peers {
		if p == self || !turned && store.contacts[p] != nil {
			continue
		}
		l := store.listed[p]
		if l == nil || turned {
			l = &listing{first: up}
			if turned {
				l.after = msg.now + store.times.skew
			}
			store.listed[p] = l
		}
		l.last = up
	}
}

// heardPeers are the peers this replica heard within G, but itself. The
// caller holds store.mu.
func (store *Datastore) heardPeers() [][]byte {
	store.prune(store.times.up())
	self := peerOf(store.replica)
	var out [][]byte
	for p := range store.contacts {
		if p != self {
			out = append(out, []byte(p))
		}
	}
	slices.SortFunc(out, bytes.Compare)
	return out
}

// prune forgets the peers and incarnations not heard for G and the
// forgotten entries no longer needed. The caller holds store.mu.
func (store *Datastore) prune(up time.Duration) {
	g := gapIntervals * store.opts.RebroadcastInterval
	for p, c := range store.contacts {
		if up-c.heard > g {
			delete(store.contacts, p)
		}
	}
	for inc, f := range store.frontiers {
		if up-f.heard > g {
			delete(store.frontiers, inc)
		}
	}
	for p, l := range store.listed {
		if up-l.last > g || store.contacts[p] != nil && (l.after == 0 || up-l.first >= g) {
			delete(store.listed, p)
		}
	}
	store.pruneForgotten(up)
}

// holds reports whether this replica holds all that a context says its
// sender had merged: every span of every entry, but an entry it forgot by
// expiry, which it holds as well as it can: its horizon is past what the
// entry last changed, or it held all of it when it dropped it and what made
// a peer keep it longer is in the entries of the removals that touched it.
// The caller holds store.mu.
func (store *Datastore) holds(o *causalContext) bool {
	now := store.dropClock()
	for r, k := range o.inc {
		if store.cc.inc[r].contains(k) {
			continue
		}
		if store.expired(k.expires, now) && store.horizon >= k.changed {
			continue
		}
		if f := store.forgotten[r]; f != nil && f.k.contains(k) {
			continue
		}
		return false
	}
	return true
}

// settled reports whether this replica holds all that every live peer said
// it holds, at least one is live, none is new to it and unsettled, none
// that is not settled names a peer it has not heard for less than G, and it
// is grounded. The caller holds store.mu.
func (store *Datastore) settled() bool {
	up := store.times.up()
	store.prune(up)
	if store.rejoining || store.since == 0 {
		return false
	}
	g := gapIntervals * store.opts.RebroadcastInterval
	live, vouched, held := false, false, true
	for _, f := range store.frontiers {
		live = true
		if !f.settled && (up-f.first < g || store.waitsFor(f, up, g)) {
			return false
		}
		if !store.holds(f.cc) {
			held = false
			continue
		}
		vouched = vouched || f.settled
	}
	if !live || !held {
		return false
	}
	if vouched || up-store.contactFrom >= g {
		store.grounded = true
	}
	return store.grounded
}

// waitsFor reports whether a frontier names a peer listed less than G ago
// that this replica has not heard, or that a frontier named on turning and
// has not sent a frontier since. The caller holds store.mu.
func (store *Datastore) waitsFor(f *frontier, up, g time.Duration) bool {
	self := peerOf(store.replica)
	for _, p := range f.peers {
		if p == self {
			continue
		}
		if l := store.listed[p]; l != nil && up-l.first < g && (l.after != 0 || store.contacts[p] == nil) {
			return true
		}
	}
	return false
}

// settle is settled, and records the fleet time this replica is settled at.
// The caller holds store.mu.
func (store *Datastore) settle(ctx context.Context) bool {
	if !store.settled() {
		return false
	}
	if err := store.setSettled(ctx, store.since); err != nil {
		store.logger.Errorf("recording the settled time: %s", err)
		return false
	}
	return true
}

func (store *Datastore) setSettled(ctx context.Context, at uint64) error {
	if at == store.settledSince {
		return nil
	}
	if err := store.store.Put(ctx, store.settledKey(), binary.AppendUvarint(nil, at)); err != nil {
		return err
	}
	store.settledSince = at
	return nil
}

// pruneForgotten drops the entries forgotten long enough ago that a peer
// still sending them would have touched them by now, unless a live peer
// sends them. The caller holds store.mu.
func (store *Datastore) pruneForgotten(up time.Duration) {
	keep := store.opts.DeliveryBound + store.opts.ClockSkew + gapIntervals*store.opts.RebroadcastInterval
	for r, f := range store.forgotten {
		if up-f.at <= keep {
			continue
		}
		sent := false
		for _, fr := range store.frontiers {
			if fr.cc.inc[r] != nil {
				sent = true
				break
			}
		}
		if !sent {
			delete(store.forgotten, r)
		}
	}
}

// vouching is the fleet time as of which this replica can vouch for what it
// holds, whether it can, and whether it is settled now. While it hears live
// peers it can only once it holds all they hold, as of the fleet time it has
// heard; rejoining or hearing none, it vouches as of the time it last did.
// The caller holds store.mu.
func (store *Datastore) vouching(ctx context.Context) (uint64, bool, bool) {
	if store.rejoining {
		return store.gapFrom, true, false
	}
	if store.liveHeard != 0 && store.times.up()-store.liveHeard <= gapIntervals*store.opts.RebroadcastInterval {
		if !store.settle(ctx) {
			return 0, false, false
		}
		return store.since, true, true
	}
	return store.held(), true, false
}

// heardFrom keeps the ok of a judge that is still heard. The caller holds
// store.mu.
func (store *Datastore) heardFrom(inc string) {
	if o := store.oks[inc]; o != nil {
		o.heard = store.times.up()
	}
}

// endRejoinIfHeld ends the rejoin once this replica holds what a judge
// vouched with, and drops the oks of judges not heard for G. The caller
// holds store.mu.
func (store *Datastore) endRejoinIfHeld(ctx context.Context) error {
	if !store.rejoining {
		return nil
	}
	up := store.times.up()
	for inc, o := range store.oks {
		if up-o.heard > gapIntervals*store.opts.RebroadcastInterval {
			delete(store.oks, inc)
			continue
		}
		if !store.holds(o.cc) {
			continue
		}
		if err := store.endRejoin(ctx, o.since); err != nil {
			return err
		}
		if err := store.setSettled(ctx, o.held); err != nil {
			return err
		}
		store.grounded = true
		store.logger.Infof("rejoined: holds what %x vouched for", inc)
		return nil
	}
	return nil
}
